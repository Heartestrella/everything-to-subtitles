package main

import (
	"strings"
	"testing"
)

func TestRenderSubtitles(t *testing.T) {
	input := []Segment{{Start: 1.2345, End: 3.5, Text: "Hello", Translation: "你好"},
		{Start: 4, End: 5, Text: "World", Translation: ""}}
	srt := renderSubtitles(input, "srt", "bilingual")
	if !strings.Contains(srt, "00:00:01,235 --> 00:00:03,500\nHello\n你好") {
		t.Fatalf("unexpected SRT: %q", srt)
	}
	vtt := renderSubtitles(input, "vtt", "translation")
	if !strings.HasPrefix(vtt, "WEBVTT\n\n00:00:01.235 --> 00:00:03.500\n你好") {
		t.Fatalf("unexpected VTT: %q", vtt)
	}
	if strings.Contains(vtt, "World") {
		t.Fatalf("untranslated segment leaked: %q", vtt)
	}
}
