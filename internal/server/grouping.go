package server

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// frame is one parsed line of a Roblox stack trace.
type frame struct {
	Script   string `json:"script"`
	Line     int    `json:"line"`
	Function string `json:"function,omitempty"`
	Raw      string `json:"raw"`
	// Set when a release sourcemap maps the instance to a source file.
	File string `json:"file,omitempty"`
	URL  string `json:"url,omitempty"`
}

var (
	// "ServerScriptService.Combat.Weapons:42: attempt to index nil with 'Ammo'"
	locationPrefix = regexp.MustCompile(`^([^\s:][^:]*?):(\d+): (.*)$`)

	// Modern:  "ServerScriptService.Combat, line 42 - function fire"
	// Legacy:  "Script 'ServerScriptService.Combat', Line 42 - function fire"
	// xpcall/debug.traceback: "ServerScriptService.Combat:42 function fire"
	frameComma     = regexp.MustCompile(`^(?:Script ')?(.+?)'?,\s*[Ll]ine (\d+)(?:\s*-\s*(.*))?$`)
	frameTraceback = regexp.MustCompile(`^(.+?):(\d+)(?:\s+(?:function\s+)?(.*))?$`)

	guidPattern   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	hexPattern    = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`)
	numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parseStack parses a Roblox stack trace (ScriptContext.Error or debug.traceback).
func parseStack(stack string) []frame {
	var frames []frame
	for _, line := range strings.Split(stack, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Stack Begin" || line == "Stack End" || strings.HasPrefix(line, "stack traceback") {
			continue
		}
		f := frame{Raw: line}
		if m := frameComma.FindStringSubmatch(line); m != nil {
			f.Script, f.Line, f.Function = m[1], atoi(m[2]), m[3]
		} else if m := frameTraceback.FindStringSubmatch(line); m != nil {
			f.Script, f.Line, f.Function = m[1], atoi(m[2]), m[3]
		} else {
			f.Script = line
		}
		f.Function = strings.TrimPrefix(strings.TrimSpace(f.Function), "function ")
		frames = append(frames, f)
	}
	return frames
}

// splitMessage separates the "Script.Path:line: " prefix Roblox prepends to
// runtime errors from the error text itself.
func splitMessage(msg string) (location, text string) {
	first, _, _ := strings.Cut(msg, "\n")
	if m := locationPrefix.FindStringSubmatch(first); m != nil {
		return m[1] + ":" + m[2], m[3]
	}
	return "", first
}

// normalizeMessage strips volatile parts of an error message (numbers, GUIDs)
// so that errors differing only in those values group together.
func normalizeMessage(text string) string {
	text = guidPattern.ReplaceAllString(text, "<guid>")
	text = hexPattern.ReplaceAllString(text, "<hex>")
	text = numberPattern.ReplaceAllString(text, "<n>")
	return strings.TrimSpace(text)
}

type grouping struct {
	Fingerprint string
	Title       string
	Culprit     string
}

// groupError decides which issue an error belongs to. Errors group by their
// normalized message plus the script/function names of the stack (line numbers
// are excluded so that unrelated edits to a script don't split the issue).
// A client-supplied fingerprint overrides this.
func groupError(side, message, stack, custom string) grouping {
	location, text := splitMessage(message)
	frames := parseStack(stack)

	culprit := location
	if culprit == "" && len(frames) > 0 {
		f := frames[0]
		culprit = f.Script
		if f.Line > 0 {
			culprit += ":" + strconv.Itoa(f.Line)
		}
	}
	title := text
	if len(title) > 200 {
		title = title[:200] + "…"
	}
	if title == "" {
		title = "(empty error message)"
	}

	h := sha256.New()
	if custom != "" {
		h.Write([]byte("custom\x00" + custom))
	} else {
		h.Write([]byte(side + "\x00" + normalizeMessage(text) + "\x00"))
		if location != "" {
			script, _, _ := strings.Cut(location, ":")
			h.Write([]byte(script + "\x00"))
		}
		for i, f := range frames {
			if i >= 8 {
				break
			}
			h.Write([]byte(f.Script + "|" + f.Function + "\x00"))
		}
	}
	return grouping{
		Fingerprint: hex.EncodeToString(h.Sum(nil))[:32],
		Title:       title,
		Culprit:     culprit,
	}
}
