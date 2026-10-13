package server

import "testing"

func TestGroupErrorMergesVolatileValues(t *testing.T) {
	stack := "ServerScriptService.Data, line 57 - function save\n"
	a := groupError("server", "ServerScriptService.Data:57: request dropped. Key = Player_123", stack, "")
	b := groupError("server", "ServerScriptService.Data:57: request dropped. Key = Player_98765", stack, "")
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("errors differing only in an id should group together")
	}
}

func TestGroupErrorIgnoresLineNumbers(t *testing.T) {
	a := groupError("server", "ServerScriptService.Combat:42: attempt to index nil with 'Ammo'", "ServerScriptService.Combat, line 42 - function fire\n", "")
	b := groupError("server", "ServerScriptService.Combat:44: attempt to index nil with 'Ammo'", "ServerScriptService.Combat, line 44 - function fire\n", "")
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("an edit that shifts line numbers should not split an issue")
	}
	if a.Title != "attempt to index nil with 'Ammo'" || a.Culprit != "ServerScriptService.Combat:42" {
		t.Fatalf("unexpected title/culprit: %q / %q", a.Title, a.Culprit)
	}
}

func TestGroupErrorSeparatesDistinctErrors(t *testing.T) {
	stack := "ServerScriptService.Combat, line 42 - function fire\n"
	cases := []grouping{
		groupError("server", "ServerScriptService.Combat:42: attempt to index nil with 'Ammo'", stack, ""),
		groupError("server", "ServerScriptService.Combat:42: attempt to index nil with 'Clip'", stack, ""),
		groupError("client", "ServerScriptService.Combat:42: attempt to index nil with 'Ammo'", stack, ""),
		groupError("server", "ServerScriptService.Shop:42: attempt to index nil with 'Ammo'", "ServerScriptService.Shop, line 42 - function buy\n", ""),
		groupError("server", "anything", stack, "custom-key"),
	}
	seen := map[string]int{}
	for i, g := range cases {
		if j, dup := seen[g.Fingerprint]; dup {
			t.Fatalf("case %d and %d should not group together", j, i)
		}
		seen[g.Fingerprint] = i
	}
}

func TestParseStackFormats(t *testing.T) {
	frames := parseStack("Stack Begin\nScript 'Workspace.Part.Script', Line 3 - function touched\nServerScriptService.Main, line 10\nReplicatedStorage.Util:7 function helper\nStack End")
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d: %+v", len(frames), frames)
	}
	want := []frame{
		{Script: "Workspace.Part.Script", Line: 3, Function: "touched"},
		{Script: "ServerScriptService.Main", Line: 10},
		{Script: "ReplicatedStorage.Util", Line: 7, Function: "helper"},
	}
	for i, w := range want {
		f := frames[i]
		if f.Script != w.Script || f.Line != w.Line || f.Function != w.Function {
			t.Errorf("frame %d = %+v, want %+v", i, f, w)
		}
	}
}
