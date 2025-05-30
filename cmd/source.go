package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/ogen-go/ogen/gen/genfs"
)

type Source struct {
	genfs.FormattedSource
}

func (s Source) WriteFile(name string, content []byte) error {
	if name == "oas_json_gen.go" {
		var err error
		content, err = commentOutJSON(content)
		if err != nil {
			return err
		}
	}
	content, err := addOmitemptyToOptStructs(content)
	if err != nil {
		return err
	}
	err = s.FormattedSource.WriteFile(name, content)
	if err != nil {
		return err
	}
	return nil
}

func commentOutJSON(content []byte) ([]byte, error) {
	node, err := parser.ParseFile(token.NewFileSet(), "", content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	type region struct{ start, end int }
	var regions []region
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			// レシーバ型名を取得
			if starExpr, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
				if ident, ok := starExpr.X.(*ast.Ident); ok {
					if strings.HasPrefix(ident.Name, "Opt") && (fn.Name.Name == "MarshalJSON" || fn.Name.Name == "UnmarshalJSON") {
						regions = append(regions, region{
							start: int(fn.Pos()) - 1,
							end:   int(fn.End()) - 1,
						})
					}
				}
			} else if ident, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				if strings.HasPrefix(ident.Name, "Opt") && (fn.Name.Name == "MarshalJSON" || fn.Name.Name == "UnmarshalJSON") {
					regions = append(regions, region{
						start: int(fn.Pos()) - 1,
						end:   int(fn.End()) - 1,
					})
				}
			}
		}
		return true
	})

	if len(regions) == 0 {
		return content, nil
	}

	// regionsを開始位置でソート
	sort.Slice(regions, func(i, j int) bool { return regions[i].start < regions[j].start })

	var out []byte
	prev := 0
	for _, r := range regions {
		if prev < r.start {
			out = append(out, content[prev:r.start]...)
		}
		out = append(out, []byte("/*\n")...)
		out = append(out, content[r.start:r.end]...)
		out = append(out, []byte("\n*/")...)
		prev = r.end
	}
	if prev < len(content) {
		out = append(out, content[prev:]...)
	}
	return out, nil
}

func addOmitemptyToOptStructs(content []byte) ([]byte, error) {
	node, err := parser.ParseFile(token.NewFileSet(), "", content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var edits []struct {
		start, end int
		tag        string
	}

	ast.Inspect(node, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			// フィールド型がOptXXXかどうかを判定
			var typeName string
			switch t := field.Type.(type) {
			case *ast.Ident:
				typeName = t.Name
			case *ast.StarExpr:
				if ident, ok := t.X.(*ast.Ident); ok {
					typeName = ident.Name
				}
			}
			if strings.HasPrefix(typeName, "Opt") {
				if field.Tag != nil {
					tag := field.Tag.Value
					if strings.Contains(tag, "json:") && !strings.Contains(tag, "omitempty") {
						re := regexp.MustCompile(`json:"([^"]+)"`)
						newTag := re.ReplaceAllString(tag, `json:"$1,omitempty"`)
						edits = append(edits, struct {
							start, end int
							tag        string
						}{int(field.Tag.Pos()) - 1, int(field.Tag.End()) - 1, newTag})
					}
				} else {
					// タグがない場合は各フィールド名ごとに、その直後にタグを追加
					for _, name := range field.Names {
						insertPos := int(name.End())
						tag := " `json:\"" + name.Name + ",omitempty\"`"
						edits = append(edits, struct {
							start, end int
							tag        string
						}{insertPos, insertPos, tag})
					}
				}
			}
		}
		return true
	})

	if len(edits) == 0 {
		return content, nil
	}

	// editsを後ろから適用
	out := content
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		out = append(out[:e.start], append([]byte(e.tag), out[e.end:]...)...)
	}
	return out, nil
}
