// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"bytes"
	"encoding/gob"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

type fixtureImporter map[string]*types.Package

func (i fixtureImporter) Import(path string) (*types.Package, error) {
	if p := i[path]; p != nil {
		return p, nil
	}
	return importer.Default().Import(path)
}

func TestRegistrationAnalysis(t *testing.T) {
	imports := fixtureImporter{}
	program := &PackageFact{Functions: map[string]*Function{}, Types: map[string]*Type{}, Globals: map[string]*Expr{}}
	add := func(path, source string) {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "fixture.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Instances: map[*ast.Ident]types.Instance{}}
		pkg, err := (&types.Config{Importer: imports}).Check(path, fset, []*ast.File{file}, info)
		if err != nil {
			t.Fatal(err)
		}
		imports[path] = pkg
		fact := summarize(&analysis.Pass{Pkg: pkg, Fset: fset, Files: []*ast.File{file}, TypesInfo: info})
		// Exercise the actual fact serialization boundary used by analysis hosts.
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(fact); err != nil {
			t.Fatal(err)
		}
		var decoded PackageFact
		if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		for k, v := range decoded.Functions {
			program.Functions[k] = v
		}
		for k, v := range decoded.Types {
			program.Types[k] = v
		}
		for k, v := range decoded.Globals {
			program.Globals[k] = v
		}
	}
	add(repo+"internal/api/coreapi", `package coreapi; type A struct{};type B struct{};type Controller struct{}`)
	add(repo+"internal/api/fleetapi", `package fleetapi;type ManagementCluster struct{}`)
	add(repo+"internal/database/cosmosstorage/kubeappliercosmosstorage", `package kubeappliercosmosstorage
import "github.com/Azure/ARO-HCP/internal/api/fleetapi"
type ManagementClusterLister interface{List()([]*fleetapi.ManagementCluster,error)}
type KubeApplierDBClient interface{}
type KubeApplierDBClients interface{For()KubeApplierDBClient}
type kubeApplierDBClients struct{mcLister ManagementClusterLister}
func(c *kubeApplierDBClients)For()KubeApplierDBClient{c.mcLister.List();return nil}`)
	add(repo+"internal/controllerregistry", `package controllerregistry
type Runnable interface{Run()};type Registration[C any] struct{Instantiate func(C)(Runnable,error);Enabled func(C)bool}`)
	add(repo+"internal/database/cosmosstorage/cosmosstorageutils", `package cosmosstorageutils
type ResourceCRUD[T any] interface{Get()(*T,error);Replace(*T)error;Delete()error;AddCreateToTransaction(*T)error}`)
	add(repo+"internal/database/listers/corelisters", `package corelisters
import "github.com/Azure/ARO-HCP/internal/api/coreapi"
type ALister interface{Get()(*coreapi.A,error)};type BLister interface{List()([]*coreapi.B,error)}`)
	add(repo+"internal/database/informers/coreinformers", `package coreinformers
import "github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
type Informer interface{AddEventHandler(any)}
type BackendInformers interface{As()(Informer,corelisters.ALister);Bs()(Informer,corelisters.BLister)}`)
	add(repo+"backend/pkg/utils/controllerutils", `package controllerutils
import (
 "github.com/Azure/ARO-HCP/internal/api/coreapi"
 "github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)
type Syncer interface{SyncOnce()}
type Wrapper struct{syncer Syncer;status cosmosstorageutils.ResourceCRUD[coreapi.Controller]}
func New(s Syncer,status cosmosstorageutils.ResourceCRUD[coreapi.Controller])*Wrapper{return &Wrapper{s,status}}
func(c *Wrapper)Run(){c.syncer.SyncOnce();WriteController(c.status)}
func WriteController(c cosmosstorageutils.ResourceCRUD[coreapi.Controller]){c.Get();c.Replace(nil)}`)
	add(repo+"backend/pkg/helpers", `package helpers
import (
 "github.com/Azure/ARO-HCP/internal/api/coreapi"
 "github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)
func Read[T any](c cosmosstorageutils.ResourceCRUD[T]){c.Get()}
func Transaction(c cosmosstorageutils.ResourceCRUD[coreapi.B]){c.AddCreateToTransaction(nil)}
func Maybe()bool{return true}
func Both(flag bool)bool{if flag{return false};return true}`)
	add(repo+"backend/pkg/app", `package app
import (
 "github.com/Azure/ARO-HCP/internal/api/coreapi"
 "github.com/Azure/ARO-HCP/internal/controllerregistry"
 "github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
 "github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
 "github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
 "github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
 "github.com/Azure/ARO-HCP/backend/pkg/helpers"
 "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
)
type Ctx struct{I coreinformers.BackendInformers;A cosmosstorageutils.ResourceCRUD[coreapi.A];B cosmosstorageutils.ResourceCRUD[coreapi.B];Status cosmosstorageutils.ResourceCRUD[coreapi.Controller];Flag bool}
type syncer struct{a corelisters.ALister;b cosmosstorageutils.ResourceCRUD[coreapi.B]}
func(s *syncer)SyncOnce(){s.a.Get();helpers.Read(s.b);helpers.Transaction(s.b)}
type onlyA struct{a corelisters.ALister}
type embedded struct{*onlyA}
func(s *onlyA)SyncOnce(){s.a.Get()}
func(s *onlyA)Unused(){var c cosmosstorageutils.ResourceCRUD[coreapi.B];c.Get()}
func choose(c Ctx)controllerutils.Syncer{_,a:=c.I.As();if c.Flag{return &onlyA{a}};return &syncer{a,c.B}}
func New(ctx Ctx,extra coreinformers.Informer,which controllerutils.Syncer)controllerregistry.Runnable{a,_:=ctx.I.As();a.AddEventHandler(nil);if extra!=nil{extra.AddEventHandler(nil)};return controllerutils.New(which,ctx.Status)}
type direct struct{ctx Ctx}
func(d *direct)Run(){if !helpers.Both(d.ctx.Flag){return};helpers.Read(d.ctx.B);controllerutils.WriteController(d.ctx.Status)}
type callbacks struct{ctx Ctx}
func(d *callbacks)Run(){callbacks:=map[string]func(){unknownKey():func(){helpers.Read(d.ctx.A)},otherKey():func(){helpers.Read(d.ctx.B)}};callbacks[unknownKey()]()}
func unknownKey()string{return ""};func otherKey()string{return ""}
type clientLookup struct{k kubeappliercosmosstorage.KubeApplierDBClients}
func(c *clientLookup)Run(){c.k.For()}
func newControllerRegistry()map[string]controllerregistry.Registration[Ctx]{
 r:=map[string]controllerregistry.Registration[Ctx]{}
 r["missing"]=controllerregistry.Registration[Ctx]{Enabled:func(Ctx)bool{return false},Instantiate:func(c Ctx)(controllerregistry.Runnable,error){_,a:=c.I.As();return New(c,nil,&syncer{a,c.B}),nil}}
 r["unused"]=controllerregistry.Registration[Ctx]{Instantiate:func(c Ctx)(controllerregistry.Runnable,error){b,_:=c.I.Bs();_,a:=c.I.As();return New(c,b,&embedded{&onlyA{a}}),nil}}
 r["none"]=controllerregistry.Registration[Ctx]{Instantiate:func(c Ctx)(controllerregistry.Runnable,error){return &direct{c},nil}}
 r["choice"]=controllerregistry.Registration[Ctx]{Instantiate:func(c Ctx)(controllerregistry.Runnable,error){return New(c,nil,choose(c)),nil}}
 r["callbacks"]=controllerregistry.Registration[Ctx]{Instantiate:func(c Ctx)(controllerregistry.Runnable,error){return &callbacks{c},nil}}
 r["lookup"]=controllerregistry.Registration[Ctx]{Instantiate:func(c Ctx)(controllerregistry.Runnable,error){return &clientLookup{},nil}}
 return r
}`)
	result := check(program, []string{repo + "backend/pkg/app.newControllerRegistry"}, repo+"backend/pkg/app")
	want := map[string]bool{
		"backend/missing | missing-watch | " + repo + "internal/api/coreapi.B":                 true,
		"backend/unused | unused-watch | " + repo + "internal/api/coreapi.B":                   true,
		"backend/none | missing-watch | " + repo + "internal/api/coreapi.B":                    true,
		"backend/none | missing-watch | " + repo + "internal/api/coreapi.Controller":           true,
		"backend/choice | missing-watch | " + repo + "internal/api/coreapi.B":                  true,
		"backend/callbacks | missing-watch | " + repo + "internal/api/coreapi.A":               true,
		"backend/callbacks | missing-watch | " + repo + "internal/api/coreapi.B":               true,
		"backend/lookup | missing-watch | " + repo + "internal/api/fleetapi.ManagementCluster": true,
	}
	if len(result.Gaps) != 0 {
		t.Fatalf("unexpected gaps: %+v", result.Gaps)
	}
	for _, finding := range result.Findings {
		if !want[finding.Identity()] {
			t.Errorf("unexpected %s", FormatFinding(finding))
		}
		delete(want, finding.Identity())
		if len(finding.Evidence) == 0 {
			t.Error("missing evidence")
		}
		for _, ev := range finding.Evidence {
			if len(ev.Path) < 2 {
				t.Errorf("missing transitive path: %+v", ev)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("missing findings: %v; inventories: %+v", want, result.Inventories)
	}
	for _, finding := range result.Findings {
		if finding.Controller == "backend/missing" {
			if len(finding.Evidence) != 2 {
				t.Errorf("want both Get and transaction call sites: %+v", finding)
			}
			if !strings.Contains(FormatFinding(finding), "helpers.Read") {
				t.Error("missing generic helper trace")
			}
		}
	}
}
