// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	"github.com/juju/charm/v9"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmrepo/v7"
	"github.com/juju/charmrepo/v7/csclient/params"
	charmtesting "github.com/juju/charmrepo/v7/testing"
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

func (s *charmStoreRepoSuite) TestGetFileFromArchive(c *gc.C) {
	specs := []struct {
		descr               string
		storeResCode        int
		storeResContentType string
		storeRes            string
		expRes              string
		expErr              string
	}{
		{
			descr:               "store API returns not found",
			storeResCode:        404,
			storeResContentType: "application/json",
			storeRes:            `{"Message":"file \"lxd-profile.yaml\" not found in the archive","Code":"not found"}`,
			expErr:              params.ErrNotFound.Error(),
		},
		{
			descr:               "store API returns the file contents",
			storeResCode:        200,
			storeResContentType: "text/plain",
			storeRes:            `raw contents`,
			expRes:              `raw contents`,
		},
	}

	charmURL := charm.MustParseURL("cs:redis")
	for specIdx, spec := range specs {
		c.Logf("%d) %s", specIdx, spec.descr)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Add("Content-Type", spec.storeResContentType)
			w.WriteHeader(spec.storeResCode)
			_, _ = fmt.Fprint(w, spec.storeRes)
		}))
		defer srv.Close()

		st := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
			URL: srv.URL,
		})

		r, err := st.GetFileFromArchive(charmURL, "lxd-profile.yaml")
		if spec.expErr != "" {
			c.Assert(err, gc.ErrorMatches, spec.expErr)
		} else {
			got, err := ioutil.ReadAll(r)
			_ = r.Close()
			c.Assert(err, jc.ErrorIsNil)
			c.Assert(string(got), gc.Equals, spec.expRes)
		}
	}
}
