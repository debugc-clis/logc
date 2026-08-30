package main

import "testing"

func TestPrinterDecoratesLogLevels(t *testing.T) {
	printer := &printer{color: true}
	tests := []struct {
		line string
		want string
	}{
		{line: "FATAL service crashed", want: "\x1b[1;35mFATAL service crashed\x1b[0m"},
		{line: "ERROR request failed", want: "\x1b[1;31mERROR request failed\x1b[0m"},
		{line: "WARN retrying request", want: "\x1b[33mWARN retrying request\x1b[0m"},
		{line: "INFO service started", want: "\x1b[36mINFO service started\x1b[0m"},
		{line: "DEBUG cache miss", want: "\x1b[2;34mDEBUG cache miss\x1b[0m"},
	}

	for _, test := range tests {
		t.Run(test.line, func(t *testing.T) {
			if got := printer.decorate(test.line); got != test.want {
				t.Fatalf("decorate(%q) = %q, want %q", test.line, got, test.want)
			}
		})
	}
}

func TestPrinterLeavesLinesPlainWithoutColor(t *testing.T) {
	printer := &printer{color: false}
	if got := printer.decorate("INFO service started"); got != "INFO service started" {
		t.Fatalf("decorate returned %q", got)
	}
}

func TestPrinterSanitizesTerminalControlSequences(t *testing.T) {
	printer := &printer{color: false}
	input := "INFO before\x1b[2Jafter\x1b]52;c;clipboard\x07\x00"
	if got := printer.decorate(input); got != "INFO beforeafter" {
		t.Fatalf("decorate returned %q", got)
	}
}
