package ast

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseFile_Function(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "main.go", `package main

import "fmt"

func Hello(name string) string {
	return fmt.Sprintf("hello %s", name)
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if pf.Package != "main" {
		t.Errorf("Package = %q, want main", pf.Package)
	}
	if len(pf.Imports) != 1 || pf.Imports[0] != "fmt" {
		t.Errorf("Imports = %v, want [fmt]", pf.Imports)
	}
	if len(pf.Symbols) != 1 {
		t.Fatalf("Symbols count = %d, want 1", len(pf.Symbols))
	}
	sym := pf.Symbols[0]
	if sym.Name != "Hello" {
		t.Errorf("Name = %q, want Hello", sym.Name)
	}
	if sym.Kind != KindFunc {
		t.Errorf("Kind = %q, want func", sym.Kind)
	}
	if !strings.Contains(sym.Signature, "func Hello(") {
		t.Errorf("Signature = %q, missing func Hello(", sym.Signature)
	}
	if sym.StartLine != 5 || sym.EndLine != 7 {
		t.Errorf("Lines = %d-%d, want 5-7", sym.StartLine, sym.EndLine)
	}
}

func TestParseFile_Method(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "svc.go", `package svc

type Server struct {
	Port int
}

func (s *Server) Start() error {
	return nil
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	var method *Symbol
	for i := range pf.Symbols {
		if pf.Symbols[i].Kind == KindMethod {
			method = &pf.Symbols[i]
			break
		}
	}
	if method == nil {
		t.Fatal("no method found")
	}
	if method.Name != "Start" {
		t.Errorf("Name = %q, want Start", method.Name)
	}
	if method.Receiver != "*Server" {
		t.Errorf("Receiver = %q, want *Server", method.Receiver)
	}
	if !strings.Contains(method.Signature, "func Start()") {
		t.Errorf("Signature = %q, missing func Start()", method.Signature)
	}
}

func TestParseFile_Struct(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "types.go", `package types

type Config struct {
	Host string
	Port int
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(pf.Symbols) != 1 {
		t.Fatalf("Symbols count = %d, want 1", len(pf.Symbols))
	}
	sym := pf.Symbols[0]
	if sym.Kind != KindType {
		t.Errorf("Kind = %q, want type", sym.Kind)
	}
	if !strings.Contains(sym.Signature, "Host string") {
		t.Errorf("Signature = %q, missing 'Host string'", sym.Signature)
	}
	if !strings.Contains(sym.Signature, "Port int") {
		t.Errorf("Signature = %q, missing 'Port int'", sym.Signature)
	}
}

func TestParseFile_Interface(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "iface.go", `package store

type Store interface {
	Get(id string) (string, error)
	Put(id, value string) error
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(pf.Symbols) != 1 {
		t.Fatalf("Symbols count = %d, want 1", len(pf.Symbols))
	}
	sym := pf.Symbols[0]
	if sym.Kind != KindInterface {
		t.Errorf("Kind = %q, want interface", sym.Kind)
	}
	if !strings.Contains(sym.Signature, "Get(") {
		t.Errorf("Signature = %q, missing Get(", sym.Signature)
	}
	if !strings.Contains(sym.Signature, "Put(") {
		t.Errorf("Signature = %q, missing Put(", sym.Signature)
	}
}

func TestParseFile_GroupedImports(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "imports.go", `package main

import (
	"context"
	"fmt"
	"os"
)

func main() {}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"context", "fmt", "os"}
	if len(pf.Imports) != len(want) {
		t.Fatalf("Imports = %v, want %v", pf.Imports, want)
	}
	for i, w := range want {
		if pf.Imports[i] != w {
			t.Errorf("Imports[%d] = %q, want %q", i, pf.Imports[i], w)
		}
	}
}

func TestParseFile_ConstVar(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "consts.go", `package main

const MaxRetries = 3

var DefaultTimeout int
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(pf.Symbols) < 2 {
		t.Fatalf("Symbols count = %d, want >= 2", len(pf.Symbols))
	}

	var foundConst, foundVar bool
	for _, sym := range pf.Symbols {
		if sym.Kind == KindConst && sym.Name == "MaxRetries" {
			foundConst = true
		}
		if sym.Kind == KindVar && sym.Name == "DefaultTimeout" {
			foundVar = true
		}
	}
	if !foundConst {
		t.Error("missing const MaxRetries")
	}
	if !foundVar {
		t.Error("missing var DefaultTimeout")
	}
}

func TestParseFile_DocComment(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "doc.go", `package main

// Hello greets a person by name.
// It returns a formatted string.
func Hello(name string) string {
	return name
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(pf.Symbols) != 1 {
		t.Fatalf("Symbols count = %d, want 1", len(pf.Symbols))
	}
	doc := pf.Symbols[0].DocComment
	if !strings.Contains(doc, "Hello greets") {
		t.Errorf("DocComment = %q, missing 'Hello greets'", doc)
	}
	if !strings.Contains(doc, "formatted string") {
		t.Errorf("DocComment = %q, missing 'formatted string'", doc)
	}
}

func TestParseFile_Caching(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "cached.go", `package main

func Foo() {}
`)
	p := NewParser(nil)
	defer p.Close()

	pf1, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	pf2, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if pf1 != pf2 {
		t.Error("expected cache hit — got different pointers")
	}
}

func TestParseDir_SkipsVendor(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "main.go", `package main
func Main() {}
`)
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, vendorDir, "dep.go", `package dep
func Dep() {}
`)

	p := NewParser(nil)
	defer p.Close()

	files, err := p.ParseDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		if strings.Contains(f.Path, "vendor") {
			t.Errorf("vendor file included: %s", f.Path)
		}
	}
	if len(files) != 1 {
		t.Errorf("file count = %d, want 1", len(files))
	}
}

func TestParseFile_InvalidSyntax(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "bad.go", `package main

func broken( {
	this is not valid go
}
`)
	p := NewParser(nil)
	defer p.Close()

	// Should not crash — tree-sitter is error-tolerant
	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Package != "main" {
		t.Errorf("Package = %q, want main", pf.Package)
	}
}

func TestDetailedSummary_IncludesSource(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "svc.go", `package svc

func Hello(name string) string {
	return "hello " + name
}
`)
	p := NewParser(nil)
	defer p.Close()

	pf, err := p.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	detailed := pf.DetailedSummary()
	// Should contain the signature line
	if !strings.Contains(detailed, "func Hello(") {
		t.Errorf("missing signature in: %s", detailed)
	}
	// Should contain the actual source body
	if !strings.Contains(detailed, `return "hello " + name`) {
		t.Errorf("missing function body in: %s", detailed)
	}
	// Should have line numbers
	if !strings.Contains(detailed, "3:") || !strings.Contains(detailed, "4:") {
		t.Errorf("missing line numbers in: %s", detailed)
	}
}

func TestParseFiles_TargetedParsing(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.go", `package main
func A() {}
`)
	writeTemp(t, dir, "b.go", `package main
func B() {}
`)

	p := NewParser(nil)
	defer p.Close()

	files := p.ParseFiles(context.Background(), dir, []string{"a.go", "b.go"})
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}

	// Paths should be relative (as passed in)
	if files[0].Path != "a.go" {
		t.Errorf("Path = %q, want a.go", files[0].Path)
	}
}

func TestSummarizeTargeted_SplitsContext(t *testing.T) {
	target := &ParsedFile{
		Path:    "target.go",
		Package: "main",
		Symbols: []Symbol{
			{Name: "Foo", Kind: KindFunc, Signature: "func Foo()", StartLine: 3, EndLine: 5},
		},
		lines: []string{"package main", "", "func Foo() {", "\treturn", "}"},
	}
	surrounding := &ParsedFile{
		Path:    "other.go",
		Package: "main",
		Symbols: []Symbol{
			{Name: "Bar", Kind: KindFunc, Signature: "func Bar()", StartLine: 3, EndLine: 5},
		},
	}

	out := SummarizeTargeted([]*ParsedFile{target, surrounding}, []*ParsedFile{target})

	// Target section with full source
	if !strings.Contains(out, "FILES TO MODIFY") {
		t.Errorf("missing target section header in: %s", out)
	}
	if !strings.Contains(out, "return") {
		t.Errorf("missing target source body in: %s", out)
	}

	// Surrounding section with signatures only
	if !strings.Contains(out, "SURROUNDING CODEBASE") {
		t.Errorf("missing surrounding section header in: %s", out)
	}
	if !strings.Contains(out, "func Bar()") {
		t.Errorf("missing surrounding signature in: %s", out)
	}
}

func TestSummarize_Format(t *testing.T) {
	files := []*ParsedFile{
		{
			Path:    "main.go",
			Package: "main",
			Imports: []string{"fmt"},
			Symbols: []Symbol{
				{Name: "Hello", Kind: KindFunc, Signature: "func Hello()", StartLine: 5, EndLine: 7},
			},
		},
	}
	out := Summarize(files)
	if !strings.Contains(out, "== main.go (package main) ==") {
		t.Errorf("missing header in: %s", out)
	}
	if !strings.Contains(out, "imports: fmt") {
		t.Errorf("missing imports in: %s", out)
	}
	if !strings.Contains(out, "func Hello() [L5-L7]") {
		t.Errorf("missing symbol in: %s", out)
	}
}
