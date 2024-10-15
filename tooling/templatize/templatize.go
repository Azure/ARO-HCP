package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"text/template"

	"github.com/Azure/ARO-HCP/tooling/templatize/config"
)

func (opts *GenerationOptions) ExecuteTemplate(ctx context.Context) error {
	cfg := config.NewConfigProvider(opts.ConfigFile, opts.Region, opts.User)
	vars, err := cfg.GetVariables(ctx, opts.Cloud, opts.DeployEnv)
	if err != nil {
		return err
	}

	// print the vars
	for k, v := range vars {
		fmt.Println(k, v)
	}

	fileName := filepath.Base(opts.Input)

	if err := os.MkdirAll(opts.Output, os.ModePerm); err != nil {
		return err
	}

	output, err := os.Create(path.Join(opts.Output, fileName))
	if err != nil {
		return err
	}
	defer output.Close()

	tmpl, err := template.New(fileName).ParseFiles(opts.Input)
	if err != nil {
		return err
	}

	return tmpl.ExecuteTemplate(output, fileName, vars)
}
