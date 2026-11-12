package agent

import (
	"testing"
	"time"
)

func TestAwayDeadlineNeedsExplicitClockDateAndZone(t *testing.T) {
	received := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	for _, c := range []struct{ quote, want string }{
		{"Send materials by 18:00 Beijing time today.", "2026-11-13T18:00:00+08:00"},
		{"请在明日北京时间 18:00 提交。", "2026-11-14T18:00:00+08:00"},
		{"Due 2026-11-14 at 15:00 UTC.", "2026-11-14T15:00:00Z"},
		{"Due today at 18:00 UTC+08:00.", "2026-11-13T18:00:00+08:00"},
		{"Due tomorrow afternoon.", ""},
		{"Due at 18:00 CST today.", ""},
		{"Due at 18:00 GMT -05:00 today.", ""},
		{"Due at 18:00 GMT -5 today.", ""},
		{"Due at 08:00 pm Beijing time today.", ""},
		{"Due at 18:00 or 17:00 Beijing time today.", ""},
	} {
		if got := AwayDeadline(c.quote, received); got != c.want {
			t.Errorf("%q = %q, want %q", c.quote, got, c.want)
		}
	}
}

func TestAwayEvidenceUsesLiteralUnambiguousSource(t *testing.T) {
	received := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	deadline := "Please send the description and demo video by 2026-11-13 at 18:00 Beijing time."
	for _, test := range []struct{ name, body, modelQuote, want string }{
		{"omitted", "Hi!\r\n" + deadline + "\r\nThanks.", "", deadline},
		{"paraphrased", deadline, "Submit both by 18:00 tomorrow.", deadline},
		{"inline", "Hi! " + deadline + " Please acknowledge receipt.", "", deadline},
		{"conflicting", deadline + "\nThe deadline might instead be tomorrow afternoon.", deadline, ""},
		{"ambiguous", "Please send the demo video by tomorrow afternoon.", "", ""},
		{"tentative", "Tentative deadline is 18:00 Beijing time tomorrow.", "", ""},
		{"unrelated", "The opening ceremony starts at 18:00 Beijing time tomorrow.", "", ""},
		{"chinese", "你好！请在明日北京时间 18:00 提交。谢谢。", "", "请在明日北京时间 18:00 提交。"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := awayEvidence(test.body, test.modelQuote, received); got != test.want {
				t.Fatalf("evidence = %q, want %q", got, test.want)
			}
		})
	}
}
