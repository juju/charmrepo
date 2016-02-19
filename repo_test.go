// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"strings"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"gopkg.in/juju/charmrepo.v2-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient"
	charmtesting "gopkg.in/juju/charmrepo.v2-unstable/testing"
)

var TestCharms = charmtesting.NewRepo("internal/test-charm-repo", "quantal")

type inferRepoSuite struct{}

var _ = gc.Suite(&inferRepoSuite{})

var inferRepositoryTests = []struct {
	url           string
	localRepoPath string
	err           string
}{{
	url: "cs:trusty/django",
}, {
	url: "local:precise/wordpress",
	err: "path to local repository not specified",
}, {
	url:           "local:precise/haproxy-47",
	localRepoPath: "/tmp/repo-path",
}}

func (s *inferRepoSuite) TestInferRepository(c *gc.C) {
	for i, test := range inferRepositoryTests {
		c.Logf("test %d: %s", i, test.url)
		ref := charm.MustParseURL(test.url)
		repo, err := charmrepo.InferRepository(
			ref, charmrepo.NewCharmStoreParams{}, test.localRepoPath)
		if test.err != "" {
			c.Assert(err, gc.ErrorMatches, test.err)
			c.Assert(repo, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		switch store := repo.(type) {
		case *charmrepo.LocalRepository:
			c.Assert(store.Path, gc.Equals, test.localRepoPath)
		case *charmrepo.CharmStore:
			c.Assert(store.URL(), gc.Equals, csclient.ServerURL)
		default:
			c.Fatal("unknown repository type")
		}
	}
}

type DispatcherSuite struct {
	testing.IsolationSuite

	stub     *testing.Stub
	factory  *stubFactory
	handlers *stubDispatchHandlers
}

var _ = gc.Suite(&DispatcherSuite{})

func (s *DispatcherSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)

	s.stub = &testing.Stub{}
	s.factory = &stubFactory{Stub: s.stub}
	s.handlers = &stubDispatchHandlers{Stub: s.stub}
}

func (s *DispatcherSuite) TestNewDispatcher(c *gc.C) {
	csParams := charmrepo.NewCharmStoreParams{}
	localPath := "/tmp/repo-path"

	dis := charmrepo.NewDispatcher(csParams, localPath)

	c.Check(dis.Factory, jc.DeepEquals, charmrepo.NewFactory(csParams, localPath))
	c.Check(dis.Handlers, jc.DeepEquals, map[string]func(string, charmrepo.Interface) error{"cs": nil, "local": nil})
}

func (s *DispatcherSuite) TestInferRecognized(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: s.handlers.asMap(),
	}
	for _, urlStr := range []string{"cs:trusty/django", "local:precise/wordpress"} {
		c.Logf("testing %q", urlStr)
		ref := charm.MustParseURL(urlStr)
		s.stub.ResetCalls()
		kind := "CharmStore"
		if strings.HasPrefix(urlStr, "local:") {
			kind = "Local"
		}

		inferred, err := dis.Infer(ref.Schema)
		c.Assert(err, jc.ErrorIsNil)

		s.stub.CheckNoCalls(c)
		c.Check(inferred.Schema, gc.Equals, ref.Schema)
		repo, err := inferred.NewRepo()
		c.Assert(err, jc.ErrorIsNil)
		s.stub.CheckCallNames(c, kind)
		s.stub.ResetCalls()
		err = inferred.Handler(ref.Schema, repo)
		c.Assert(err, jc.ErrorIsNil)
		s.stub.CheckCallNames(c, "Handle"+kind)
	}
}

func (s *DispatcherSuite) TestInferNoOp(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: map[string]func(string, charmrepo.Interface) error{"cs": nil, "local": nil},
	}

	inferred, err := dis.Infer("cs")
	c.Assert(err, jc.ErrorIsNil)

	s.stub.CheckNoCalls(c)
	c.Check(inferred.Schema, gc.Equals, "cs")
	repo, err := inferred.NewRepo()
	c.Assert(err, jc.ErrorIsNil)
	s.stub.CheckCallNames(c, "CharmStore")
	s.stub.ResetCalls()
	err = inferred.Handler("cs", repo)
	c.Assert(err, jc.ErrorIsNil)
	s.stub.CheckNoCalls(c)
}

func (s *DispatcherSuite) TestInferUnrecognized(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: s.handlers.asMap(),
	}

	_, err := dis.Infer("<unknown>")

	s.stub.CheckNoCalls(c)
	c.Check(err, gc.ErrorMatches, `unrecognized charm schema "<unknown>"`)
}

func (s *DispatcherSuite) TestInferUnsupported(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: nil,
	}

	_, err := dis.Infer("cs")

	s.stub.CheckNoCalls(c)
	c.Check(err, gc.ErrorMatches, `unsupported charm schema "cs"`)
}

func (s *DispatcherSuite) TestDispatchCharmStore(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: s.handlers.asMap(),
	}

	err := dis.Dispatch("cs")
	c.Assert(err, jc.ErrorIsNil)

	s.stub.CheckCallNames(c, "CharmStore", "HandleCharmStore")
}

func (s *DispatcherSuite) TestDispatchLocal(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: s.handlers.asMap(),
	}

	err := dis.Dispatch("local")
	c.Assert(err, jc.ErrorIsNil)

	s.stub.CheckCallNames(c, "Local", "HandleLocal")
}

func (s *DispatcherSuite) TestDispatchNoOp(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: map[string]func(string, charmrepo.Interface) error{"cs": nil},
	}

	err := dis.Dispatch("cs")
	c.Assert(err, jc.ErrorIsNil)

	s.stub.CheckCallNames(c, "CharmStore")
}

func (s *DispatcherSuite) TestDispatchUnrecognized(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: s.handlers.asMap(),
	}

	err := dis.Dispatch("<unknown>")

	s.stub.CheckNoCalls(c)
	c.Check(err, gc.ErrorMatches, `unrecognized charm schema "<unknown>"`)
}

func (s *DispatcherSuite) TestDispatchUnsupported(c *gc.C) {
	dis := charmrepo.Dispatcher{
		Factory:  s.factory,
		Handlers: nil,
	}

	err := dis.Dispatch("cs")

	s.stub.CheckNoCalls(c)
	c.Check(err, gc.ErrorMatches, `unsupported charm schema "cs"`)
}

type FactorySuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&FactorySuite{})

func (s *FactorySuite) TestNewFactory(c *gc.C) {
}

type stubFactory struct {
	*testing.Stub

	ReturnCharmStore charmrepo.Interface
	ReturnLocal      charmrepo.Interface
}

func (s *stubFactory) CharmStore() (charmrepo.Interface, error) {
	s.AddCall("CharmStore")
	if err := s.NextErr(); err != nil {
		return nil, err
	}

	return s.ReturnCharmStore, nil
}

func (s *stubFactory) Local() (charmrepo.Interface, error) {
	s.AddCall("Local")
	if err := s.NextErr(); err != nil {
		return nil, err
	}

	return s.ReturnLocal, nil
}

type stubDispatchHandlers struct {
	*testing.Stub
}

func (s *stubDispatchHandlers) asMap() map[string]func(string, charmrepo.Interface) error {
	return map[string]func(string, charmrepo.Interface) error{
		"cs":    s.HandleCharmStore,
		"local": s.HandleLocal,
	}
}

func (s *stubDispatchHandlers) HandleCharmStore(schema string, repo charmrepo.Interface) error {
	s.AddCall("HandleCharmStore", schema, repo)
	if err := s.NextErr(); err != nil {
		return err
	}

	return nil
}

func (s *stubDispatchHandlers) HandleLocal(schema string, repo charmrepo.Interface) error {
	s.AddCall("HandleLocal", schema, repo)
	if err := s.NextErr(); err != nil {
		return err
	}

	return nil
}
