package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func stamp(seconds float64, format string) string {
	total := int64(math.Round(math.Max(0, seconds) * 1000))
	hours := total / 3600000
	minutes := (total / 60000) % 60
	secs := (total / 1000) % 60
	millis := total % 1000
	separator := ","
	if format == "vtt" {
		separator = "."
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", hours, minutes, secs, separator, millis)
}

func parseSubtitleFile(data, ext string) ([]Segment, error) {
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r\n", "\n"), "\r", "\n")
	blocks := strings.Split(strings.TrimSpace(data), "\n\n")
	stampRE := regexp.MustCompile(`(?m)(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})`)
	segments := make([]Segment, 0)
	for _, block := range blocks {
		m := stampRE.FindStringSubmatch(block)
		if len(m) != 9 {
			continue
		}
		toSec := func(h, min, sec, ms string) float64 {
			hv, _ := strconv.ParseFloat(h, 64)
			mv, _ := strconv.ParseFloat(min, 64)
			sv, _ := strconv.ParseFloat(sec, 64)
			msv, _ := strconv.ParseFloat(ms, 64)
			return hv*3600 + mv*60 + sv + msv/1000
		}
		lines := strings.Split(block, "\n")
		textLines := make([]string, 0)
		for _, line := range lines {
			if !strings.Contains(line, "-->") && !regexp.MustCompile(`^\d+$`).MatchString(strings.TrimSpace(line)) && strings.TrimSpace(line) != "" {
				textLines = append(textLines, strings.TrimSpace(line))
			}
		}
		if len(textLines) > 0 {
			segments = append(segments, Segment{Start: toSec(m[1], m[2], m[3], m[4]), End: toSec(m[5], m[6], m[7], m[8]), Text: strings.Join(textLines, "\n")})
		}
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("无法解析字幕文件")
	}
	return segments, nil
}

func cleanCaption(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func renderSubtitles(segments []Segment, format, track string) string {
	var result strings.Builder
	if format == "ass" {
		result.WriteString("[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H64000000,0,0,0,0,100,100,0,0,1,2,0,2,40,40,30,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	}
	if format == "vtt" {
		result.WriteString("WEBVTT\n\n")
	}
	number := 0
	for _, segment := range segments {
		original := cleanCaption(segment.Text)
		translation := cleanCaption(segment.Translation)
		var lines []string
		switch track {
		case "original":
			if original != "" {
				lines = append(lines, original)
			}
		case "translation":
			if translation != "" {
				lines = append(lines, translation)
			}
		case "bilingual":
			if original != "" {
				lines = append(lines, original)
			}
			if translation != "" && translation != original {
				lines = append(lines, translation)
			}
		}
		if len(lines) == 0 {
			continue
		}
		number++
		if format == "srt" {
			fmt.Fprintf(&result, "%d\n", number)
		} else if format == "ass" {
			text := strings.ReplaceAll(strings.Join(lines, "\\N"), "{", "\\{")
			fmt.Fprintf(&result, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", stampASS(segment.Start), stampASS(segment.End), text)
			continue
		}
		fmt.Fprintf(&result, "%s --> %s\n%s\n\n", stamp(segment.Start, format), stamp(segment.End, format), strings.Join(lines, "\n"))
	}
	return result.String()
}

func stampASS(seconds float64) string {
	total := int64(math.Round(math.Max(0, seconds)))
	return fmt.Sprintf("%d:%02d:%02d", total/3600, (total/60)%60, total%60)
}
