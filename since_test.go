package main

import (
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"
)

func TestParseSinceRelativeWindows(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"1ns", time.Nanosecond},
		{"1.5us", 1500 * time.Nanosecond},
		{"2ms", 2 * time.Millisecond},
		{"30s", 30 * time.Second},
		{"10m", 10 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"+2h", 2 * time.Hour},
		{"1d", 24 * time.Hour},
		{"1.5d", 36 * time.Hour},
		{".25d", 6 * time.Hour},
		{"+2d", 48 * time.Hour},
		{"1e-1d", 144 * time.Minute},
		{"1w", 7 * 24 * time.Hour},
		{"1.5w", 252 * time.Hour},
		{".25w", 42 * time.Hour},
		{"+2w", 14 * 24 * time.Hour},
		{"1e-1w", 1008 * time.Minute},
		{"0.00000000000002d", time.Nanosecond},
		{"0.000000000000002w", time.Nanosecond},
		{"9223372036854775807ns", time.Duration(1<<63 - 1)},
	}
	for _, unit := range []struct {
		suffix string
		scale  time.Duration
	}{{"d", 24 * time.Hour}, {"w", 7 * 24 * time.Hour}} {
		value := float64(0x1p63) / float64(unit.scale)
		for value*float64(unit.scale) >= 0x1p63 {
			value = math.Nextafter(value, 0)
		}
		tests = append(tests, struct {
			raw  string
			want time.Duration
		}{strconv.FormatFloat(value, 'g', -1, 64) + unit.suffix, time.Duration(value * float64(unit.scale))})
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			before := time.Now()
			got, err := parseSince(test.raw)
			after := time.Now()
			if err != nil {
				t.Fatalf("parseSince(%q): %v", test.raw, err)
			}
			if got.Before(before.Add(-test.want)) || got.After(after.Add(-test.want)) {
				t.Fatalf("parseSince(%q) = %v; want now minus %v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseSinceInvalidWindows(t *testing.T) {
	inputs := []string{
		"0", "0s", "-1h", "0.1ns", "9223372036854775808ns",
		"d", "w", "1D", "1W", "1d2h", "1w2d", " 1d", "1d ",
		"yesterday", "2026-02-30", "2026-11-04T25:00:00Z",
	}
	for _, suffix := range []string{"d", "w"} {
		for _, value := range []string{
			"0", "+0", "-0", "0.0", "-1", "-0.5", "NaN", "nan",
			"Inf", "+Inf", "-Inf", "Infinity", "+Infinity", "-Infinity",
			"1e309", "-1e309", "1e300", "1e20", "1e-300", "1e-999", "1e-15",
		} {
			inputs = append(inputs, value+suffix)
		}
		scale := float64(24 * time.Hour)
		if suffix == "w" {
			scale = float64(7 * 24 * time.Hour)
		}
		boundary := float64(0x1p63) / scale
		for boundary*scale < 0x1p63 {
			boundary = math.Nextafter(boundary, math.Inf(1))
		}
		inputs = append(inputs, strconv.FormatFloat(boundary, 'g', -1, 64)+suffix)
		inputs = append(inputs, strconv.FormatFloat(math.Nextafter(boundary, math.Inf(1)), 'g', -1, 64)+suffix)
	}
	for _, raw := range inputs {
		t.Run(raw, func(t *testing.T) {
			got, err := parseSince(raw)
			wantError := fmt.Sprintf("invalid --since %q", raw)
			if err == nil || err.Error() != wantError {
				t.Fatalf("parseSince(%q) = %v, %v; want %s", raw, got, err, wantError)
			}
			if !got.IsZero() {
				t.Fatalf("parseSince(%q) returned nonzero time %v on error", raw, got)
			}
		})
	}
}

func TestParseSinceAbsoluteAndSpecial(t *testing.T) {
	local := time.Local
	tests := []struct {
		raw  string
		want time.Time
	}{
		{"", time.Time{}},
		{"2026-11-04", time.Date(2026, 11, 4, 0, 0, 0, 0, local)},
		{"2026-11-04 12:34:56", time.Date(2026, 11, 4, 12, 34, 56, 0, local)},
		{"2026-11-04T12:34:56Z", time.Date(2026, 11, 4, 12, 34, 56, 0, time.UTC)},
		{"2026-11-04T12:34:56.123456789+05:30", time.Date(2026, 11, 4, 12, 34, 56, 123456789, time.FixedZone("", 19800))},
		{"2999-01-01", time.Date(2999, 1, 1, 0, 0, 0, 0, local)},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got, err := parseSince(test.raw)
			if err != nil || !got.Equal(test.want) {
				t.Fatalf("parseSince(%q) = %v, %v; want %v", test.raw, got, err, test.want)
			}
		})
	}
	for _, raw := range []string{"today", "TODAY", "ToDaY"} {
		t.Run(raw, func(t *testing.T) {
			before := time.Now()
			got, err := parseSince(raw)
			after := time.Now()
			midnight := func(value time.Time) time.Time {
				return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
			}
			if err != nil || (!got.Equal(midnight(before)) && !got.Equal(midnight(after))) || got.Location() != local {
				t.Fatalf("parseSince(%q) = %v, %v; want local midnight", raw, got, err)
			}
		})
	}
}
