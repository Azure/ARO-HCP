// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
)

// ValidateSimpleFieldAccess parses content as a Go template and verifies that
// it only uses simple field access ({{ .field.name }}).
//
// Allowed: plain text, simple and nested field access ({{ .foo }}, {{ .foo.bar }}).
// Rejected: anything else, including conditionals (if), loops (range), with blocks, pipes, function calls,
// variable declarations/references, template invocations, literal values, and
// dot-only access ({{ . }})
//
// Returns an error listing all violations with line numbers if any are found.
func ValidateSimpleFieldAccess(content []byte) error {
	tmpl, err := template.New("").Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var violations []string
	for _, t := range tmpl.Templates() {
		if t.Tree != nil && t.Root != nil {
			violations = append(violations, collectViolations(t.Root, content)...)
		}
	}

	if len(violations) == 0 {
		return nil
	}

	return fmt.Errorf("template contains restricted constructs:\n  %s", strings.Join(violations, "\n  "))
}

func collectViolations(node parse.Node, content []byte) []string {
	if node == nil {
		return nil
	}

	var violations []string
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, child := range n.Nodes {
			violations = append(violations, collectViolations(child, content)...)
		}
	case *parse.TextNode:
		// literal text is always allowed
	case *parse.ActionNode:
		violations = append(violations, validateAction(n, content)...)
	case *parse.IfNode:
		violations = append(violations, fmt.Sprintf("line %d: if conditional not allowed", posToLine(content, n.Position())))
		violations = append(violations, collectViolations(n.List, content)...)
		violations = append(violations, collectViolations(n.ElseList, content)...)
	case *parse.RangeNode:
		violations = append(violations, fmt.Sprintf("line %d: range loop not allowed", posToLine(content, n.Position())))
		violations = append(violations, collectViolations(n.List, content)...)
		violations = append(violations, collectViolations(n.ElseList, content)...)
	case *parse.WithNode:
		violations = append(violations, fmt.Sprintf("line %d: with block not allowed", posToLine(content, n.Position())))
		violations = append(violations, collectViolations(n.List, content)...)
		violations = append(violations, collectViolations(n.ElseList, content)...)
	case *parse.TemplateNode:
		violations = append(violations, fmt.Sprintf("line %d: template invocation not allowed", posToLine(content, n.Position())))
	default:
		violations = append(violations, fmt.Sprintf("line %d: unsupported template construct %T", posToLine(content, n.Position()), n))
	}
	return violations
}

func validateAction(action *parse.ActionNode, content []byte) []string {
	pipe := action.Pipe
	line := posToLine(content, action.Position())

	var violations []string

	if len(pipe.Decl) != 0 {
		names := make([]string, 0, len(pipe.Decl))
		for _, decl := range pipe.Decl {
			names = append(names, decl.Ident[0])
		}
		violations = append(violations, fmt.Sprintf("line %d: variable declaration %s not allowed", line, strings.Join(names, ", ")))
	}

	if len(pipe.Cmds) > 1 {
		violations = append(violations, fmt.Sprintf("line %d: pipe not allowed", line))
		return violations
	}

	if len(pipe.Cmds) == 0 {
		return violations
	}

	cmd := pipe.Cmds[0]

	if len(cmd.Args) == 1 {
		if _, ok := cmd.Args[0].(*parse.FieldNode); ok {
			return violations
		}
	}

	if len(cmd.Args) > 1 {
		if ident, ok := cmd.Args[0].(*parse.IdentifierNode); ok {
			violations = append(violations, fmt.Sprintf("line %d: function call %q not allowed", line, ident.Ident))
		} else {
			violations = append(violations, fmt.Sprintf("line %d: complex expression not allowed, only field access ({{ .field }}) permitted", line))
		}
	} else if len(cmd.Args) == 1 {
		switch arg := cmd.Args[0].(type) {
		case *parse.IdentifierNode:
			violations = append(violations, fmt.Sprintf("line %d: function call %q not allowed", line, arg.Ident))
		case *parse.VariableNode:
			violations = append(violations, fmt.Sprintf("line %d: variable reference %q not allowed", line, arg.Ident[0]))
		case *parse.DotNode:
			violations = append(violations, fmt.Sprintf("line %d: dot-only access not allowed, use a named field", line))
		case *parse.StringNode:
			violations = append(violations, fmt.Sprintf("line %d: literal value not allowed, only field access ({{ .field }}) permitted", line))
		case *parse.NumberNode:
			violations = append(violations, fmt.Sprintf("line %d: literal value not allowed, only field access ({{ .field }}) permitted", line))
		case *parse.BoolNode:
			violations = append(violations, fmt.Sprintf("line %d: literal value not allowed, only field access ({{ .field }}) permitted", line))
		case *parse.NilNode:
			violations = append(violations, fmt.Sprintf("line %d: literal value not allowed, only field access ({{ .field }}) permitted", line))
		default:
			violations = append(violations, fmt.Sprintf("line %d: unsupported construct not allowed, only field access ({{ .field }}) permitted", line))
		}
	}

	return violations
}

func posToLine(content []byte, pos parse.Pos) int {
	offset := max(0, min(int(pos), len(content)))
	return 1 + bytes.Count(content[:offset], []byte{'\n'})
}
