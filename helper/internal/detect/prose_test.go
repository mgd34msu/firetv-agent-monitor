package detect

import "testing"

func TestDescribeCommand(t *testing.T) {
	cases := map[string]string{
		"rg -n foo src":                      "Searching the code",
		"sed -n '1,20p' packages/app/api.ts": "Reading api.ts",
		"sleep 25; tail -18 build.log":       "Reading build.log",
		"cd web && npm test":                 "Running tests",
		"git status --short":                 "Running git status",
		"FOO=1 make build":                   "Running make build",
	}
	for cmd, want := range cases {
		if got := DescribeCommand(cmd); got != want {
			t.Errorf("%q: got %q, want %q", cmd, got, want)
		}
	}
}
