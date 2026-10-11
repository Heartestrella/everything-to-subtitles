"""GPU worker: WhisperX transcription/alignment followed by NLLB translation.

Usage: python worker.py input.mp4 config.json result.json
The process prints JSON stage events; the result file is written atomically.
"""

import gc
import json
import os
import sys
from pathlib import Path


LANGUAGES = {
    "en": "eng_Latn", "zh": "zho_Hans", "zh-tw": "zho_Hant",
    "ja": "jpn_Jpan", "ko": "kor_Hang", "fr": "fra_Latn",
    "de": "deu_Latn", "es": "spa_Latn", "ru": "rus_Cyrl",
    "pt": "por_Latn", "it": "ita_Latn", "ar": "arb_Arab",
    "hi": "hin_Deva", "id": "ind_Latn", "vi": "vie_Latn",
    "th": "tha_Thai", "tr": "tur_Latn", "uk": "ukr_Cyrl",
}


def event(stage, detail=""):
    print(json.dumps({"stage": stage, "detail": detail}, ensure_ascii=False), flush=True)


def run(source, config_path, result_path):
    config = json.loads(Path(config_path).read_text(encoding="utf-8"))
    import torch
    import whisperx

    if not torch.cuda.is_available():
        raise RuntimeError("未检测到 CUDA GPU；请检查 Colab GPU 配额或本机 CUDA 环境")
    device = "cuda"
    event("transcribing", "正在识别语音")
    model = whisperx.load_model(config.get("model", "medium"), device,
                                compute_type=config.get("compute_type", "float16"))
    audio = whisperx.load_audio(source)
    raw = model.transcribe(audio, batch_size=int(config.get("batch_size", 8)),
                           language=config.get("source_language") or None)
    del model
    gc.collect()
    torch.cuda.empty_cache()

    language = raw.get("language", "")
    event("aligning", "正在校准时间轴")
    alignment = "forced"
    alignment_note = ""
    try:
        aligner, metadata = whisperx.load_align_model(language_code=language, device=device)
        aligned = whisperx.align(raw["segments"], aligner, metadata, audio, device,
                                 return_char_alignments=False)
        segments = aligned["segments"]
        del aligner
        gc.collect()
        torch.cuda.empty_cache()
    except (ValueError, KeyError) as exc:
        # Some WhisperX languages lack an alignment model. Preserve segment timing.
        event("aligning", f"该语言暂不支持强制对轴，使用识别时间轴：{exc}")
        alignment = "transcription"
        alignment_note = str(exc)
        segments = raw["segments"]

    target = config.get("target_language", "zho_Hans")
    source_lang = LANGUAGES.get(language)
    output = []
    for segment in segments:
        start = max(0.0, float(segment.get("start", 0)))
        end = max(start + 0.01, float(segment.get("end", start + 0.01)))
        original = segment.get("text", "").strip()
        if original:
            output.append({"start": start, "end": end,
                           "text": original, "translation": ""})

    if target == source_lang:
        for segment in output:
            segment["translation"] = segment["text"]
    elif target and source_lang and output:
        event("translating", "正在翻译字幕")
        from transformers import AutoModelForSeq2SeqLM, AutoTokenizer
        name = config.get("translation_model", "facebook/nllb-200-distilled-600M")
        tokenizer = AutoTokenizer.from_pretrained(name, src_lang=source_lang)
        translator = AutoModelForSeq2SeqLM.from_pretrained(name).to(device)
        for i in range(0, len(output), 8):
            block = output[i:i + 8]
            tokens = tokenizer([s["text"] for s in block], return_tensors="pt",
                               padding=True, truncation=True, max_length=384).to(device)
            with torch.inference_mode():
                ids = translator.generate(**tokens,
                    forced_bos_token_id=tokenizer.convert_tokens_to_ids(target),
                    max_new_tokens=192)
            for segment, translated in zip(block, tokenizer.batch_decode(ids, skip_special_tokens=True)):
                segment["translation"] = translated.strip()
            event("translating", f"已翻译 {min(i + 8, len(output))}/{len(output)} 条")
    elif target and not source_lang:
        raise RuntimeError(f"检测到语言 {language}，当前尚无对应的 NLLB 语言映射")

    result = {"language": language, "target_language": target,
              "alignment": alignment, "alignment_note": alignment_note,
              "segments": output}
    path = Path(result_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    os.replace(temporary, path)
    event("complete", f"完成 {len(output)} 条字幕")


if __name__ == "__main__":
    if len(sys.argv) != 4:
        raise SystemExit("usage: worker.py INPUT CONFIG RESULT")
    run(*sys.argv[1:])
