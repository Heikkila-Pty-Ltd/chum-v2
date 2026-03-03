package ast

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
)

func TestMeasureFilterEfficiency(t *testing.T) {
	parser := NewParser(slog.Default())
	ctx := context.Background()

	files, err := parser.ParseDir(ctx, "/home/ubuntu/projects/chum")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	tasks := []string{
		"add rate limiting to the HTTP endpoint",
		"fix the user authentication login flow and JWT token validation",
		"add embedding-based filtering for AST parsed files",
		"fix profile registration and fitness scoring",
	}

	for _, task := range tasks {
		t.Run(task, func(t *testing.T) {
			// Full source = what you'd paste without AST
			fullSourceSize := 0
			for _, f := range files {
				fullSourceSize += len(f.DetailedSummary())
			}

			// AST signatures only = no filtering, just structural
			sigOnlySize := 0
			for _, f := range files {
				sigOnlySize += len(f.Summary())
			}

			// Keyword filter: relevant get full source, rest get signatures
			relKW, surKW := FilterRelevant(task, files)
			kwSize := 0
			for _, f := range relKW {
				kwSize += len(f.DetailedSummary())
			}
			for _, f := range surKW {
				kwSize += len(f.Summary())
			}

			// Embedding filter: same approach but semantic matching
			// Only test with a small subset to avoid timeout
			ef := NewEmbedFilter()
			relEmb, surEmb := ef.FilterRelevantByEmbedding(ctx, task, files[:10])
			embSize := 0
			for _, f := range relEmb {
				embSize += len(f.DetailedSummary())
			}
			for _, f := range surEmb {
				embSize += len(f.Summary())
			}

			fmt.Printf("\n=== Task: %s ===\n", task)
			fmt.Printf("Total files: %d\n", len(files))
			fmt.Printf("Full source (no AST):     %6d chars (~%5d tokens)\n", fullSourceSize, fullSourceSize/4)
			fmt.Printf("AST signatures only:      %6d chars (~%5d tokens) [%.0f%% saved]\n",
				sigOnlySize, sigOnlySize/4,
				100.0*(1.0-float64(sigOnlySize)/float64(fullSourceSize)))
			fmt.Printf("Keyword targeted:         %6d chars (~%5d tokens) [%.0f%% saved, %d relevant / %d surrounding]\n",
				kwSize, kwSize/4,
				100.0*(1.0-float64(kwSize)/float64(fullSourceSize)),
				len(relKW), len(surKW))
			fmt.Printf("Embed (10-file sample):   %6d chars (~%5d tokens) [%d relevant / %d surrounding]\n",
				embSize, embSize/4, len(relEmb), len(surEmb))
		})
	}
}
