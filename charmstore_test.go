// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	charmtesting "github.com/juju/charmrepo/v7/testing"
	"github.com/juju/charmrepo/v7"
	"github.com/juju/charmrepo/v7/csclient/params"
)

type charmStoreRepoSuite struct {
	jujutesting.IsolationSuite
}

var TestCharms = charmtesting.NewRepo("storetests/internal/test-charm-repo", "quantal")

var _ = gc.Suite(&charmStoreRepoSuite{})

var sortChannelsTests = []struct {
	input  []params.Channel
	sorted []params.Channel
}{{
	input:  []params.Channel{params.StableChannel, params.CandidateChannel, params.EdgeChannel},
	sorted: []params.Channel{params.StableChannel, params.CandidateChannel, params.EdgeChannel},
}, {
	input:  []params.Channel{params.DevelopmentChannel, params.StableChannel},
	sorted: []params.Channel{params.StableChannel, params.DevelopmentChannel},
}, {
	input:  []params.Channel{params.StableChannel, params.DevelopmentChannel},
	sorted: []params.Channel{params.StableChannel, params.DevelopmentChannel},
}, {
	input:  []params.Channel{params.UnpublishedChannel, params.DevelopmentChannel},
	sorted: []params.Channel{params.DevelopmentChannel, params.UnpublishedChannel},
}, {
	input:  []params.Channel{params.StableChannel, params.Channel("brand-new"), params.BetaChannel},
	sorted: []params.Channel{params.StableChannel, params.Channel("brand-new"), params.BetaChannel},
}, {
	input:  []params.Channel{params.StableChannel},
	sorted: []params.Channel{params.StableChannel},
}, {
	input:  []params.Channel{params.DevelopmentChannel},
	sorted: []params.Channel{params.DevelopmentChannel},
}, {
	input:  []params.Channel{params.UnpublishedChannel},
	sorted: []params.Channel{params.UnpublishedChannel},
}, {
	// No channels provided.
}}

func (s *charmStoreRepoSuite) TestSortChannels(c *gc.C) {
	for i, test := range sortChannelsTests {
		c.Logf("\ntest %d: %v", i, test.input)
		charmrepo.SortChannels(test.input)
		c.Assert(test.input, jc.DeepEquals, test.sorted)
	}
}
