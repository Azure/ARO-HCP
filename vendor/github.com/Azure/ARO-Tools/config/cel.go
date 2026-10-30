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
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

type celVocabulary struct {
	env *cel.Env
}

// NewCELVocabulary creates a jsonschema vocabulary that evaluates CEL expressions
// defined in x-cel-validations schema annotations, mirroring Kubernetes CRD
// x-kubernetes-validations. Each rule is an object with "rule" (a CEL expression
// returning bool) and "message" (the error shown on failure). Rules are evaluated
// against the value at the schema node where they are declared, bound to "self".
//
// Available CEL extensions: semver() constructor with comparison operators and
// accessors, plus cel-go's strings and lists extensions.
func NewCELVocabulary() (*jsonschema.Vocabulary, error) {
	env, err := cel.NewEnv(
		cel.Variable("self", cel.DynType),
		cel.HomogeneousAggregateLiterals(),
		cel.EagerlyValidateDeclarations(true),
		cel.DefaultUTCTimeZone(true),

		ext.Strings(ext.StringsVersion(2)),
		ext.Lists(),

		semvers(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}
	cv := &celVocabulary{env: env}
	return &jsonschema.Vocabulary{
		URL:     "https://github.com/Azure/ARO-Tools/cel-validation",
		Compile: cv.compile,
	}, nil
}

type celRule struct {
	program    cel.Program
	expression string
	message    string
}

// celExtension implements jsonschema.SchemaExt for CEL validation.
type celExtension struct {
	rules []celRule
}

const celValidationsKey = "x-cel-validations"

// celErrorKind implements jsonschema.ErrorKind for CEL validation failures.
type celErrorKind struct {
	message string
}

func (e *celErrorKind) KeywordPath() []string {
	return []string{celValidationsKey}
}

func (e *celErrorKind) LocalizedString(_ *message.Printer) string {
	return e.message
}

func (cv *celVocabulary) compile(_ *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
	rawRules, ok := obj[celValidationsKey]
	if !ok {
		return nil, nil
	}

	rulesSlice, ok := rawRules.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", celValidationsKey)
	}

	var rules []celRule
	for i, rawRule := range rulesSlice {
		ruleObj, ok := rawRule.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: must be an object", celValidationsKey, i)
		}

		ruleExpr, ok := ruleObj["rule"].(string)
		if !ok || ruleExpr == "" {
			return nil, fmt.Errorf("%s[%d]: missing \"rule\" field", celValidationsKey, i)
		}

		ruleMessage, ok := ruleObj["message"].(string)
		if !ok || ruleMessage == "" {
			return nil, fmt.Errorf("%s[%d]: missing \"message\" field", celValidationsKey, i)
		}

		ast, issues := cv.env.Parse(ruleExpr)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("%s[%d]: failed to parse CEL expression: %w", celValidationsKey, i, issues.Err())
		}

		checked, issues := cv.env.Check(ast)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("%s[%d]: failed to check CEL expression: %w", celValidationsKey, i, issues.Err())
		}

		if checked.OutputType() != cel.BoolType {
			return nil, fmt.Errorf("%s[%d]: CEL expression must return bool, got %s", celValidationsKey, i, checked.OutputType())
		}

		program, err := cv.env.Program(checked)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: failed to compile CEL program: %w", celValidationsKey, i, err)
		}

		rules = append(rules, celRule{
			program:    program,
			expression: ruleExpr,
			message:    ruleMessage,
		})
	}

	return &celExtension{rules: rules}, nil
}

// Validate evaluates all CEL rules against the value v.
func (c *celExtension) Validate(ctx *jsonschema.ValidatorContext, v any) {
	for _, rule := range c.rules {
		result, _, err := rule.program.Eval(map[string]any{"self": v})
		if err != nil {
			ctx.AddError(&celErrorKind{
				message: fmt.Sprintf("CEL evaluation error for rule %q: %v", rule.expression, err),
			})
			continue
		}

		b, ok := result.Value().(bool)
		if !ok {
			ctx.AddError(&celErrorKind{
				message: fmt.Sprintf("CEL rule %q returned non-bool value: %T", rule.expression, result.Value()),
			})
			continue
		}

		if !b {
			ctx.AddError(&celErrorKind{
				message: rule.message,
			})
		}
	}
}
