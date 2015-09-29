// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

type LegacyLocalRepository legacyLocalRepository

var NewLocalRepository = newLocalRepository
var NewCharmPath = newCharmPath

func MaybeLocalRepository(repo Interface) (*legacyLocalRepository, bool) {
	if repo, ok := repo.(*legacyLocalRepository); ok {
		return repo, ok
	}
	return nil, false
}

func MaybeCharmPath(repo Interface) (*charmPath, bool) {
	if repo, ok := repo.(*charmPath); ok {
		return repo, ok
	}
	return nil, false
}
