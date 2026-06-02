package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

func runModels(args []string, w io.Writer) error {
	opts, err := parseCommonOptions("models", args)
	if err != nil {
		return err
	}
	_, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, err := requireProvider(sel)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		marker := " "
		if m.ID == sel.Model {
			marker = "*"
		}
		fmt.Fprintf(w, "%s %s\n", marker, m.ID)
	}
	return nil
}
