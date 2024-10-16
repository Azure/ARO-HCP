package main

import (
	"fmt"
	"log"
	"text/template"
)

func (opts *GenerationOptions) ExecuteTemplate() error {
	// print the vars
	for k, v := range opts.Config {
		fmt.Println(k, v)
	}

	tmpl, err := template.New(opts.InputFile).ParseFS(opts.Input, opts.InputFile)
	if err != nil {
		return err
	}

	defer func() {
		if err := opts.Output.Close(); err != nil {
			log.Printf("error closing output: %v\n", err)
		}
	}()
	return tmpl.ExecuteTemplate(opts.Output, opts.InputFile, opts.Config)
}
