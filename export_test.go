// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo // import "gopkg.in/juju/charmrepo.v2-unstable"

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
)

var SortChannels = sortChannels

func GetCacheDir(c *gc.C, cs *CharmStore) string {
	result, err := cs.getCacheDir()
	c.Assert(err, jc.ErrorIsNil)
	return result
}
