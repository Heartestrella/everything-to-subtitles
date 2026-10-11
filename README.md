# 声轨 · Subtitle Studio

跨平台本地 WebUI 字幕工作台。Go 提供任务队列、文件存储和导出；Vue 3 提供上传、状态、媒体预览和字幕校对；Python 使用 WhisperX 识别与对轴，使用 NLLB-200 翻译。默认先通过官方 `google-colab-cli` 使用 Colab GPU，失败时尝试本机 CUDA GPU。媒体和字幕记录保存在运行本服务的电脑上；选择 Colab 时，媒体会上传到该 Colab 会话用于处理。

## 启动

需要 Go 1.23+、Node.js 20.19+/22.12+ 和 FFmpeg。本机 GPU 路径另需 Python、CUDA/PyTorch；Colab 路径会使用项目受管的 Python 3.12 环境。启动命令在项目根目录执行：

```powershell
cd web
npm install
npm run build
cd ..
go run .
```

打开 `http://127.0.0.1:8765`。服务默认只监听本机，任务数据写入 `data/`，不会随重启丢失。上传文件上限 4 GB。`go run . -listen 127.0.0.1:9000 -data /path/to/data` 可更改监听地址和数据目录。

开发前端可在另一个终端运行 `cd web && npm run dev`，Vite 会将 `/api` 代理到 Go 服务。

## Colab GPU（优先）

程序首次使用 Colab 时会将[官方 google-colab-cli](https://github.com/googlecolab/google-colab-cli)自动安装到 `data/tools/colabcli/`，无需全局安装 `colab` 命令。优先使用已安装的 `uv` 自动准备 Python 3.12；没有 `uv` 时尝试系统 Python 3.12。首次安装需要网络。在 WebUI 点击“连接 Colab”，打开 Google 授权页，登录后把页面显示的授权码填回本机 WebUI。软件不会将授权码写入任务记录或日志。也可以在项目根目录使用终端方式：

```powershell
go run . colab sessions
```

该子命令会启动软件内置的 Colab CLI。程序为字幕任务申请 T4，上传媒体及工作脚本，在 Colab 安装模型依赖，执行 WhisperX/NLLB-200，下载结果，然后停止会话。Colab GPU 是否可用取决于账号配额和当时资源；初次运行还会下载模型，耗时较长。自动模式中如果 Colab 执行失败，会尝试本机 GPU。Google 凭据由官方 CLI 保存在本机用户目录。

目前受管安装固定为 `google-colab-cli 0.7.4`。该版本在 Windows 启动时会提前导入 Unix 专用控制台模块，软件仅在受管环境内将此导入延迟到 `colab console` 命令，以便字幕用到的非交互命令运行；不修改系统 Python 或全局 CLI。

可用 `SUBTITLE_COLAB` 指定已有 CLI，或用 `SUBTITLE_COLAB_PYTHON` 指定用于内置环境的 Python 3.12+。使用自定义数据目录时，CLI 子命令需设置 `SUBTITLE_DATA_DIR` 为同一目录。

## 本机 GPU（回退或指定）

安装与 CUDA 驱动兼容的 PyTorch 后安装工作进程依赖。PyTorch/CUDA 安装命令请使用[官方选择器](https://pytorch.org/get-started/locally/)按本机配置生成。然后：

```powershell
python -m pip install -r worker/requirements.txt
python -c "import torch; print(torch.cuda.is_available())"
```

最后一条应输出 `True`。如果要指定虚拟环境中的 Python，设置 `SUBTITLE_PYTHON` 为可执行文件路径后启动 Go 服务。服务本身、WebUI 和文件导出可在 Windows、macOS、Linux 运行；本机推理目前需要 CUDA。Colab 路径可在没有本机 GPU 的电脑上运行。

## 工作流程

1. 上传媒体，源语言默认自动识别，手动选择目标语言。
2. WhisperX 识别并尝试强制对轴。不支持对轴的语言会使用识别时间轴，并在任务结果中标记。
3. NLLB-200 distilled 600M 按字幕片段翻译。识别语言暂未映射到 NLLB 代码时任务会明确失败，避免误把原文当译文。
4. 在工作台逐条修改原文、译文和起止时间，保存后导出 SRT 或 WebVTT。支持双语、原文、译文轨道。

当前翻译语言见界面选项。字幕翻译保留原文时间轴，译文不会二次音频对轴。首次使用时模型权重从模型仓库下载，后续由模型库缓存。

## 边界与许可证

- 本项目尚未在真实 Colab GPU 或本机 CUDA 上完成模型端到端验证。`go test ./...`、前端构建和 Python 语法检查只验证服务、导出与构建路径。
- [NLLB-200 distilled 600M 模型](https://huggingface.co/facebook/nllb-200-distilled-600M)采用 **CC BY-NC 4.0**，模型卡说明它面向研究，不适用于生产部署。商业使用应先更换获准的翻译模型并验证效果与许可证。
- 浏览器未必能预览 MKV 等文件，但后台仍可通过 FFmpeg 处理。
- 默认绑定 `127.0.0.1`。若将服务公开到网络，需要先增加认证、HTTPS 和上传保护。

## 验证

```powershell
go test ./...
python -m py_compile worker/worker.py
cd web; npm run build
```
