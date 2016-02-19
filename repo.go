// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package charmrepo implements access to charm repositories.

package charmrepo

import (
	"fmt"

	"github.com/juju/loggo"
	"gopkg.in/juju/charm.v6-unstable"
)

var logger = loggo.GetLogger("juju.charm.charmrepo")

// Interface represents a charm repository (a collection of charms).
type Interface interface {
	// Get returns the charm referenced by curl.
	Get(curl *charm.URL) (charm.Charm, error)

	// GetBundle returns the bundle referenced by curl.
	GetBundle(curl *charm.URL) (charm.Bundle, error)

	// Resolve resolves the given reference to a canonical form which refers
	// unambiguously to a specific revision of an entity. If the entity
	// is a charm that may support more than one series, canonRef.Series will
	// be empty and supportedSeries will hold the list of series supported by
	// the charm with the preferred series first.
	// If ref holds a series, then Resolve will always ensure that the returned
	// entity supports that series.
	Resolve(ref *charm.URL) (canonRef *charm.URL, supportedSeries []string, err error)
}

// InferRepository returns a charm repository inferred from the provided charm
// or bundle reference.
// Charm store references will use the provided parameters.
// Local references will use the provided path.
func InferRepository(ref *charm.URL, charmStoreParams NewCharmStoreParams, localRepoPath string) (Interface, error) {
	dis := NewDispatcher(charmStoreParams, localRepoPath)
	inferred, err := dis.Infer(ref.Schema)
	if err != nil {
		return nil, err
	}
	repo, err := inferred.NewRepo()
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// Dispatcher supports selecting and performing repo-related behavior
// based on charm schemas. Behavior selection is independent of the
// source of the schema (e.g. charm.URL).
//
// Recognized schemas are:
//   cs
//   local
type Dispatcher struct {
	// Factory is the charm repo factory that the dispatcher uses.
	Factory Factory

	// Handlers maps charm schemas to functions that will be called
	// in response to the corresponding schema. A missing entry
	// (schema not found in the map) indicates that the schema is not
	// supported. If the entry is there but holds a nil function
	// then that indicates a no-op.
	Handlers map[string]func(schema string, repo Interface) error
}

// NewDispatcher returns a new repo dispatcher with no-op handlers for
// each of the recognized schemas. The dispatcher's repo factory is
// created using the provided charm store and local repo info.
func NewDispatcher(charmStoreParams NewCharmStoreParams, localRepoPath string) *Dispatcher {
	return &Dispatcher{
		Factory:  NewFactory(charmStoreParams, localRepoPath),
		Handlers: map[string]func(string, Interface) error{"cs": nil, "local": nil},
	}
}

// Infer determines the repo and handler to use for the given schema.
// It does not execute any behavior relative to the repo.
func (dis Dispatcher) Infer(schema string) (*Inferred, error) {
	var newRepo func() (Interface, error)
	switch schema {
	case "cs":
		newRepo = dis.Factory.CharmStore
	case "local":
		newRepo = dis.Factory.Local
	default:
		// TODO fix this error message to reference bundles too?
		return nil, fmt.Errorf("unrecognized charm schema %q", schema)
	}

	handler, ok := dis.Handlers[schema]
	if !ok {
		return nil, fmt.Errorf("unsupported charm schema %q", schema)
	}
	if handler == nil {
		// Use a no-op handler.
		handler = func(string, Interface) error { return nil }
	}

	inferred := &Inferred{
		Schema:  schema,
		NewRepo: newRepo,
		Handler: handler,
	}
	return inferred, nil
}

// Dispatch infers the repo and handler from the provided schema. Then
// it calls the handler, returning its result.
func (dis Dispatcher) Dispatch(schema string) error {
	inferred, err := dis.Infer(schema)
	if err != nil {
		return err
	}
	repo, err := inferred.NewRepo()
	if err != nil {
		return err
	}
	if err := inferred.Handler(schema, repo); err != nil {
		return err
	}
	return nil
}

// Inferred holds the information for a schema as determined by
// a Dispatcher.
type Inferred struct {
	// Schema is the inferred schema.
	Schema string

	// NewRepo returns the charm repo that should be used for the schema.
	NewRepo func() (Interface, error)

	// Handler is the handler that should be used for the repo.
	Handler func(schema string, repo Interface) error
}

// Factory exposes factory methods for the different kinds of
// charm repo.
type Factory interface {
	// CharmStore returns a charm store repo.
	CharmStore() (Interface, error)

	// Local returns a local repo.
	Local() (Interface, error)
}

// NewFactory returns a repo factory that produces repo clients
// using the provided charm store and local repo details.
func NewFactory(charmStoreParams NewCharmStoreParams, localRepoPath string) Factory {
	return &repoFactory{
		csParams:      charmStoreParams,
		localRepoPath: localRepoPath,
	}
}

type repoFactory struct {
	csParams      NewCharmStoreParams
	localRepoPath string
}

// CharmStore implements Factory.
func (factory repoFactory) CharmStore() (Interface, error) {
	return NewCharmStore(factory.csParams), nil
}

// Local implements Factory.
func (factory repoFactory) Local() (Interface, error) {
	return NewLocalRepository(factory.localRepoPath)
}
