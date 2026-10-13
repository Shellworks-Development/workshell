package server

import (
	"encoding/xml"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

// handleSDKModel serves the Luau SDK as a .rbxmx model that can be dragged
// straight into Roblox Studio: a "Workshell" ModuleScript (init.luau) with
// the other modules as children, mirroring sdk/default.project.json.
func (s *Server) handleSDKModel(w http.ResponseWriter, r *http.Request) error {
	model, err := buildRbxmx(s.sdk)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="Workshell.rbxmx"`)
	_, err = w.Write(model)
	return err
}

func buildRbxmx(sdk fs.FS) ([]byte, error) {
	root, err := fs.ReadFile(sdk, "init.luau")
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(sdk, ".")
	if err != nil {
		return nil, err
	}
	var children []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".luau") && e.Name() != "init.luau" {
			children = append(children, e.Name())
		}
	}
	sort.Strings(children)

	var b strings.Builder
	b.WriteString(`<roblox xmlns:xmime="http://www.w3.org/2005/05/xmlmime" version="4">` + "\n")
	ref := 0
	writeItem := func(name, source string, open bool) {
		ref++
		b.WriteString(`<Item class="ModuleScript" referent="RBX` + strconv.Itoa(ref) + `"><Properties>`)
		b.WriteString(`<string name="Name">`)
		xml.EscapeText(&b, []byte(name))
		b.WriteString(`</string><ProtectedString name="Source">`)
		xml.EscapeText(&b, []byte(source))
		b.WriteString(`</ProtectedString></Properties>`)
		if !open {
			b.WriteString("</Item>\n")
		}
	}
	writeItem("Workshell", string(root), true)
	for _, name := range children {
		src, err := fs.ReadFile(sdk, name)
		if err != nil {
			return nil, err
		}
		writeItem(strings.TrimSuffix(path.Base(name), ".luau"), string(src), false)
	}
	b.WriteString("</Item>\n</roblox>\n")
	return []byte(b.String()), nil
}
