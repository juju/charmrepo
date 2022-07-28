// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo_test // import "github.com/juju/charmrepo/v6"

import (
	"testing"

	mgotesting "github.com/juju/mgo/v2/testing"
)

func TestPackage(t *testing.T) {
	mgotesting.MgoTestPackage(t, nil)
}
