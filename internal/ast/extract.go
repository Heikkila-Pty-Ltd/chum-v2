package ast

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extractFile walks the root node of a parsed Go file and extracts
// package name, imports, and top-level symbols.
func extractFile(root *sitter.Node, src []byte) (pkg string, imports []string, symbols []Symbol) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "package_clause":
			pkg = extractPackageName(child, src)
		case "import_declaration":
			imports = append(imports, extractImports(child, src)...)
		case "function_declaration":
			if sym, ok := extractFunction(child, src); ok {
				symbols = append(symbols, sym)
			}
		case "method_declaration":
			if sym, ok := extractMethod(child, src); ok {
				symbols = append(symbols, sym)
			}
		case "type_declaration":
			symbols = append(symbols, extractTypeDecl(child, src)...)
		case "const_declaration":
			symbols = append(symbols, extractConstOrVar(child, src, KindConst)...)
		case "var_declaration":
			symbols = append(symbols, extractConstOrVar(child, src, KindVar)...)
		}
	}
	return pkg, imports, symbols
}

// extractPackageName returns the package name from a package_clause node.
func extractPackageName(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "package_identifier" {
			return child.Content(src)
		}
	}
	return ""
}

// extractImports returns import paths from an import_declaration node.
// Handles both single imports and grouped import blocks.
func extractImports(node *sitter.Node, src []byte) []string {
	var paths []string
	walkImportSpecs(node, src, &paths)
	return paths
}

func walkImportSpecs(node *sitter.Node, src []byte, paths *[]string) {
	if node.Type() == "import_spec" {
		pathNode := node.ChildByFieldName("path")
		if pathNode != nil {
			p := pathNode.Content(src)
			p = strings.Trim(p, `"`)
			*paths = append(*paths, p)
		}
		return
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		walkImportSpecs(node.NamedChild(i), src, paths)
	}
}

// extractFunction extracts a Symbol from a function_declaration node.
func extractFunction(node *sitter.Node, src []byte) (Symbol, bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return Symbol{}, false
	}
	name := nameNode.Content(src)
	params := nodeFieldText(node, "parameters", src)
	result := nodeFieldText(node, "result", src)

	sig := "func " + name + params
	if result != "" {
		sig += " " + result
	}

	return Symbol{
		Name:       name,
		Kind:       KindFunc,
		Signature:  sig,
		DocComment: extractDocComment(node, src),
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
	}, true
}

// extractMethod extracts a Symbol from a method_declaration node.
func extractMethod(node *sitter.Node, src []byte) (Symbol, bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return Symbol{}, false
	}
	name := nameNode.Content(src)
	receiver := extractReceiver(node, src)
	params := nodeFieldText(node, "parameters", src)
	result := nodeFieldText(node, "result", src)

	sig := "func " + name + params
	if result != "" {
		sig += " " + result
	}

	return Symbol{
		Name:       name,
		Kind:       KindMethod,
		Signature:  sig,
		Receiver:   receiver,
		DocComment: extractDocComment(node, src),
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
	}, true
}

// extractReceiver extracts the receiver type name from a method_declaration.
// Returns e.g. "*DAG" or "Parser".
func extractReceiver(node *sitter.Node, src []byte) string {
	recvList := node.ChildByFieldName("receiver")
	if recvList == nil {
		return ""
	}
	// receiver is a parameter_list containing one parameter_declaration
	for i := 0; i < int(recvList.NamedChildCount()); i++ {
		param := recvList.NamedChild(i)
		if param.Type() == "parameter_declaration" {
			typeNode := param.ChildByFieldName("type")
			if typeNode == nil {
				continue
			}
			return extractTypeName(typeNode, src)
		}
	}
	return ""
}

// extractTypeName resolves a type node to a string like "*Foo" or "Foo".
func extractTypeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "pointer_type":
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "type_identifier" {
				return "*" + child.Content(src)
			}
		}
		return node.Content(src)
	case "type_identifier":
		return node.Content(src)
	default:
		return node.Content(src)
	}
}

// extractTypeDecl extracts symbols from a type_declaration node.
// A single type declaration can contain multiple type_spec children.
func extractTypeDecl(node *sitter.Node, src []byte) []Symbol {
	var symbols []Symbol
	for i := 0; i < int(node.NamedChildCount()); i++ {
		spec := node.NamedChild(i)
		if spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		typeNode := spec.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}

		sym := Symbol{
			Name:       name,
			DocComment: extractDocComment(node, src),
			StartLine:  int(spec.StartPoint().Row) + 1,
			EndLine:    int(spec.EndPoint().Row) + 1,
		}

		switch typeNode.Type() {
		case "struct_type":
			sym.Kind = KindType
			sym.Signature = "type " + name + " struct { " + extractStructFields(typeNode, src) + " }"
		case "interface_type":
			sym.Kind = KindInterface
			sym.Signature = "type " + name + " interface { " + extractInterfaceMethods(typeNode, src) + " }"
		default:
			sym.Kind = KindType
			sym.Signature = "type " + name + " " + typeNode.Content(src)
		}
		symbols = append(symbols, sym)
	}
	return symbols
}

// extractStructFields returns a semicolon-separated list of "Name Type" pairs.
func extractStructFields(node *sitter.Node, src []byte) string {
	var fields []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		fieldList := node.NamedChild(i)
		if fieldList.Type() != "field_declaration_list" {
			continue
		}
		for j := 0; j < int(fieldList.NamedChildCount()); j++ {
			field := fieldList.NamedChild(j)
			if field.Type() != "field_declaration" {
				continue
			}
			nameNode := field.ChildByFieldName("name")
			typeNode := field.ChildByFieldName("type")
			if nameNode != nil && typeNode != nil {
				fields = append(fields, nameNode.Content(src)+" "+typeNode.Content(src))
			} else if typeNode != nil {
				// embedded field
				fields = append(fields, typeNode.Content(src))
			}
		}
	}
	return strings.Join(fields, "; ")
}

// extractInterfaceMethods returns a semicolon-separated list of method signatures.
func extractInterfaceMethods(node *sitter.Node, src []byte) string {
	var methods []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "method_spec", "method_elem":
			nameNode := child.ChildByFieldName("name")
			params := nodeFieldText(child, "parameters", src)
			result := nodeFieldText(child, "result", src)
			if nameNode != nil {
				sig := nameNode.Content(src) + params
				if result != "" {
					sig += " " + result
				}
				methods = append(methods, sig)
			}
		case "type_identifier", "qualified_type":
			// embedded interface
			methods = append(methods, child.Content(src))
		}
	}
	return strings.Join(methods, "; ")
}

// extractConstOrVar extracts symbol names from const or var declarations.
func extractConstOrVar(node *sitter.Node, src []byte, kind SymbolKind) []Symbol {
	var symbols []Symbol
	specType := "const_spec"
	if kind == KindVar {
		specType = "var_spec"
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		spec := node.NamedChild(i)
		if spec.Type() != specType {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		typeText := nodeFieldText(spec, "type", src)
		sig := string(kind) + " " + name
		if typeText != "" {
			sig += " " + typeText
		}
		symbols = append(symbols, Symbol{
			Name:      name,
			Kind:      kind,
			Signature: sig,
			StartLine: int(spec.StartPoint().Row) + 1,
			EndLine:   int(spec.EndPoint().Row) + 1,
		})
	}
	return symbols
}

// extractDocComment collects consecutive // comment lines immediately preceding a node.
// Capped at 3 lines to keep context compact.
func extractDocComment(node *sitter.Node, src []byte) string {
	parent := node.Parent()
	if parent == nil {
		return ""
	}

	// Find this node's index among its parent's children
	nodeIdx := -1
	for i := 0; i < int(parent.ChildCount()); i++ {
		if parent.Child(i) == node {
			nodeIdx = i
			break
		}
	}
	if nodeIdx <= 0 {
		return ""
	}

	// Walk backward collecting comment siblings
	var lines []string
	for i := nodeIdx - 1; i >= 0 && len(lines) < 3; i-- {
		sibling := parent.Child(i)
		if sibling.Type() != "comment" {
			break
		}
		text := sibling.Content(src)
		text = strings.TrimPrefix(text, "// ")
		text = strings.TrimPrefix(text, "//")
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		return ""
	}
	// Reverse since we walked backward
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

// nodeFieldText is a nil-safe helper to get text from a named field.
func nodeFieldText(node *sitter.Node, field string, src []byte) string {
	child := node.ChildByFieldName(field)
	if child == nil {
		return ""
	}
	return child.Content(src)
}
