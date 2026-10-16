package cli

import "testing"

func TestRunDispatch(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unknown command", []string{"serve"}, 2},
		{"help", []string{"help"}, 0},
		{"version", []string{"version"}, 0},
		{"legacy -version flag", []string{"-version"}, 0},
		// 参数校验在加载配置之前完成，不会触达 DB / Redis。
		{"all rejects extra args", []string{"all", "x"}, 1},
		{"api rejects extra args", []string{"api", "x"}, 1},
		{"worker rejects extra args", []string{"worker", "x"}, 1},
		{"migrate rejects unknown sub", []string{"migrate", "redo"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Run(tc.args); got != tc.want {
				t.Fatalf("Run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseMigrateArgs(t *testing.T) {
	cases := []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{nil, "up", false},
		{[]string{"up"}, "up", false},
		{[]string{"down"}, "down", false},
		{[]string{"status"}, "status", false},
		{[]string{"redo"}, "", true},
		{[]string{"up", "down"}, "", true},
	}
	for _, tc := range cases {
		got, err := parseMigrateArgs(tc.args)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Fatalf("parseMigrateArgs(%v) = %q, %v; want %q, wantErr=%v", tc.args, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestReadyAfter(t *testing.T) {
	calls := 0
	ready := readyAfter(2, func() { calls++ })
	ready()
	if calls != 0 {
		t.Fatalf("fn called after 1 of 2 signals")
	}
	ready()
	ready()
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}
