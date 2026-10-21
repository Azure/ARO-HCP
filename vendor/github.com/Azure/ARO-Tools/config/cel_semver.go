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
	"reflect"
	"strconv"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/functions"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"golang.org/x/mod/semver"
)

const celSemverTypeName = "config.Semver"

const (
	semverLessOverload = "semver_less_semver"
	semverLteOverload  = "semver_lte_semver"
	semverGtOverload   = "semver_gt_semver"
	semverGteOverload  = "semver_gte_semver"
)

var (
	celSemverType        = cel.ObjectType(celSemverTypeName, traits.ComparerType)
	celSemverRuntimeType = types.NewObjectType(celSemverTypeName, traits.ComparerType)
)

type celSemver struct {
	raw   string // canonical form with "v" prefix
	major int
	minor int
	patch int
}

func parseSemver(s string) (celSemver, error) {
	withPrefix := s
	if !strings.HasPrefix(withPrefix, "v") {
		withPrefix = "v" + withPrefix
	}
	if !semver.IsValid(withPrefix) {
		return celSemver{}, fmt.Errorf("invalid semver: %s", s)
	}

	canonical := semver.Canonical(withPrefix)
	withoutPrefix := strings.TrimPrefix(canonical, "v")
	beforePrerelease := strings.SplitN(withoutPrefix, "-", 2)[0]
	majorMinorPatch := strings.Split(beforePrerelease, ".")

	// semver.Canonical always produces "vMAJOR.MINOR.PATCH", so Atoi is safe
	major, _ := strconv.Atoi(majorMinorPatch[0])
	minor, _ := strconv.Atoi(majorMinorPatch[1])
	patch, _ := strconv.Atoi(majorMinorPatch[2])

	return celSemver{
		raw:   canonical,
		major: major,
		minor: minor,
		patch: patch,
	}, nil
}

func (s celSemver) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, fmt.Errorf("type conversion to %v not supported", typeDesc)
}

func (s celSemver) ConvertToType(typeVal ref.Type) ref.Val {
	switch typeVal {
	case types.StringType:
		return types.String(s.raw)
	case types.TypeType:
		return celSemverRuntimeType
	default:
		return types.NewErr("type conversion to %s not supported", typeVal)
	}
}

func (s celSemver) Equal(other ref.Val) ref.Val {
	o, ok := other.(celSemver)
	if !ok {
		return types.MaybeNoSuchOverloadErr(other)
	}
	return types.Bool(s.raw == o.raw)
}

func (s celSemver) Compare(other ref.Val) ref.Val {
	o, ok := other.(celSemver)
	if !ok {
		return types.MaybeNoSuchOverloadErr(other)
	}
	return types.Int(semver.Compare(s.raw, o.raw))
}

func (s celSemver) Type() ref.Type {
	return celSemverRuntimeType
}

func (s celSemver) Value() any {
	return s.raw
}

// semvers returns a CEL environment option that registers semver functions.
func semvers() cel.EnvOption {
	return cel.Lib(&semverLib{})
}

type semverLib struct{}

func semverIntAccessor(name string, accessor func(celSemver) int) cel.EnvOption {
	return cel.Function(name,
		cel.MemberOverload("semver_"+name,
			[]*cel.Type{celSemverType},
			cel.IntType,
			cel.UnaryBinding(func(val ref.Val) ref.Val {
				sv, ok := val.(celSemver)
				if !ok {
					return types.MaybeNoSuchOverloadErr(val)
				}
				return types.Int(accessor(sv))
			}),
		),
	)
}

func (l *semverLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("semver",
			cel.Overload("semver_string",
				[]*cel.Type{cel.StringType},
				celSemverType,
				cel.UnaryBinding(func(val ref.Val) ref.Val {
					s, ok := val.Value().(string)
					if !ok {
						return types.MaybeNoSuchOverloadErr(val)
					}
					sv, err := parseSemver(s)
					if err != nil {
						return types.NewErr("%s", err)
					}
					return sv
				}),
			),
		),
		cel.Function("isSemver",
			cel.Overload("isSemver_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(func(val ref.Val) ref.Val {
					s, ok := val.Value().(string)
					if !ok {
						return types.MaybeNoSuchOverloadErr(val)
					}
					_, err := parseSemver(s)
					return types.Bool(err == nil)
				}),
			),
		),
		semverIntAccessor("major", func(sv celSemver) int { return sv.major }),
		semverIntAccessor("minor", func(sv celSemver) int { return sv.minor }),
		semverIntAccessor("patch", func(sv celSemver) int { return sv.patch }),
		cel.Function("compareTo",
			cel.MemberOverload("semver_compareTo_semver",
				[]*cel.Type{celSemverType, celSemverType},
				cel.IntType,
				cel.BinaryBinding(func(lhs, rhs ref.Val) ref.Val {
					cmp, errVal := semverBinaryCompare(lhs, rhs)
					if errVal != nil {
						return errVal
					}
					return types.Int(cmp)
				}),
			),
		),
		cel.Function("_<_",
			cel.Overload(semverLessOverload,
				[]*cel.Type{celSemverType, celSemverType},
				cel.BoolType,
			),
		),
		cel.Function("_<=_",
			cel.Overload(semverLteOverload,
				[]*cel.Type{celSemverType, celSemverType},
				cel.BoolType,
			),
		),
		cel.Function("_>_",
			cel.Overload(semverGtOverload,
				[]*cel.Type{celSemverType, celSemverType},
				cel.BoolType,
			),
		),
		cel.Function("_>=_",
			cel.Overload(semverGteOverload,
				[]*cel.Type{celSemverType, celSemverType},
				cel.BoolType,
			),
		),
	}
}

func semverBinaryCompare(lhs, rhs ref.Val) (int, ref.Val) {
	left, ok := lhs.(celSemver)
	if !ok {
		return 0, types.MaybeNoSuchOverloadErr(lhs)
	}
	right, ok := rhs.(celSemver)
	if !ok {
		return 0, types.MaybeNoSuchOverloadErr(rhs)
	}
	return semver.Compare(left.raw, right.raw), nil
}

func semverCompareOp(pred func(int) bool) func(ref.Val, ref.Val) ref.Val {
	return func(lhs, rhs ref.Val) ref.Val {
		cmp, errVal := semverBinaryCompare(lhs, rhs)
		if errVal != nil {
			return errVal
		}
		return types.Bool(pred(cmp))
	}
}

// ProgramOptions provides runtime bindings for semver comparison operators.
// cel.Functions is deprecated but required here — operator overloads for
// built-in operators (_<_, _<=_, etc.) cannot use inline BinaryBinding
// in CompileOptions due to cel-go's "singleton function incompatible with
// specialized overloads" constraint.
func (l *semverLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{
		cel.Functions( //nolint:staticcheck // see comment above
			&functions.Overload{
				Operator: semverLessOverload,
				Binary:   semverCompareOp(func(cmp int) bool { return cmp < 0 }),
			},
			&functions.Overload{
				Operator: semverLteOverload,
				Binary:   semverCompareOp(func(cmp int) bool { return cmp <= 0 }),
			},
			&functions.Overload{
				Operator: semverGtOverload,
				Binary:   semverCompareOp(func(cmp int) bool { return cmp > 0 }),
			},
			&functions.Overload{
				Operator: semverGteOverload,
				Binary:   semverCompareOp(func(cmp int) bool { return cmp >= 0 }),
			},
		),
	}
}
