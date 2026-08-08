package main

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"/var/log/syslog*", "/var/log/syslog.1", true},
		{"/var/log/journal/**", "/var/log/journal/a/b.log", true},
		{"/srv/**/logs/*.log", "/srv/a/b/logs/app.log", true},
		{"/srv/**/logs/*.log", "/srv/logs/app.log", true},
		{"/srv/*/logs/*.log", "/srv/a/b/logs/app.log", false},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q)=%v want %v", c.p, c.s, got, c.want)
		}
	}
}
