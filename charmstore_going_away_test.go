// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"net/http"
	"net/http/httptest"
	"sort"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"gopkg.in/juju/charmrepo.v2-unstable"
)

func (s *charmStoreSuite) TestURL(c *gc.C) {
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: "https://1.2.3.4/charmstore",
	})
	c.Assert(repo.URL(), gc.Equals, "https://1.2.3.4/charmstore")
}

func (s *charmStoreRepoSuite) TestLatest(c *gc.C) {
	// Add some charms to the charm store.
	s.addCharm(c, "~who/trusty/mysql-0", "mysql")
	s.addCharm(c, "~who/precise/wordpress-1", "wordpress")
	s.addCharm(c, "~dalek/trusty/riak-0", "riak")
	s.addCharm(c, "~dalek/trusty/riak-1", "riak")
	s.addCharm(c, "~dalek/trusty/riak-3", "riak")
	_, url := s.addCharm(c, "~who/utopic/varnish-0", "varnish")

	// Change permissions on one of the charms so that it is not readable by
	// anyone.
	err := s.client.Put("/"+url.Path()+"/meta/perm/read", []string{"dalek"})
	c.Assert(err, jc.ErrorIsNil)

	// Calculate and store the expected hashes for the uploaded charms.
	mysqlHash := hashOfCharm(c, "mysql")
	wordpressHash := hashOfCharm(c, "wordpress")
	riakHash := hashOfCharm(c, "riak")

	// Define the tests to be run.
	tests := []struct {
		about string
		urls  []*charm.URL
		revs  []charmrepo.CharmRevision
	}{{
		about: "no urls",
	}, {
		about: "charm not found",
		urls:  []*charm.URL{charm.MustParseURL("cs:trusty/no-such-42")},
		revs: []charmrepo.CharmRevision{{
			Err: charmrepo.CharmNotFound("cs:trusty/no-such"),
		}},
	}, {
		about: "resolve",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/trusty/mysql-42"),
			charm.MustParseURL("cs:~who/trusty/mysql-0"),
			charm.MustParseURL("cs:~who/trusty/mysql"),
		},
		revs: []charmrepo.CharmRevision{{
			Revision: 0,
			Sha256:   mysqlHash,
		}, {
			Revision: 0,
			Sha256:   mysqlHash,
		}, {
			Revision: 0,
			Sha256:   mysqlHash,
		}},
	}, {
		about: "multiple charms",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/precise/wordpress"),
			charm.MustParseURL("cs:~who/trusty/mysql-47"),
			charm.MustParseURL("cs:~dalek/trusty/no-such"),
			charm.MustParseURL("cs:~dalek/trusty/riak-0"),
		},
		revs: []charmrepo.CharmRevision{{
			Revision: 1,
			Sha256:   wordpressHash,
		}, {
			Revision: 0,
			Sha256:   mysqlHash,
		}, {
			Err: charmrepo.CharmNotFound("cs:~dalek/trusty/no-such"),
		}, {
			Revision: 3,
			Sha256:   riakHash,
		}},
	}, {
		about: "unauthorized",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/precise/wordpress"),
			url,
		},
		revs: []charmrepo.CharmRevision{{
			Revision: 1,
			Sha256:   wordpressHash,
		}, {
			Err: charmrepo.CharmNotFound("cs:~who/utopic/varnish"),
		}},
	}}

	// Run the tests.
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		revs, err := s.repo.Latest(test.urls...)
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(revs, jc.DeepEquals, test.revs)
	}
}

func (s *charmStoreRepoSuite) TestGetWithTestMode(c *gc.C) {
	_, url := s.addCharm(c, "~who/precise/wordpress-42", "wordpress")

	// Use a repo with test mode enabled to download a charm a couple of
	// times, and check the downloads count is not increased.
	repo := s.repo.WithTestMode()
	_, err := repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	_, err = repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	s.checkCharmDownloads(c, url, 0)
}

func (s *charmStoreRepoSuite) TestGetWithJujuAttrs(c *gc.C) {
	_, url := s.addCharm(c, "trusty/riak-0", "riak")

	// Set up a proxy server that stores the request header.
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header
		s.handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: srv.URL,
	})

	// Make a first request without Juju attrs.
	_, err := repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(header.Get(charmrepo.JujuMetadataHTTPHeader), gc.Equals, "")

	// Make a second request after setting Juju attrs.
	repo = repo.WithJujuAttrs(map[string]string{
		"k1": "v1",
		"k2": "v2",
	})
	_, err = repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	values := header[http.CanonicalHeaderKey(charmrepo.JujuMetadataHTTPHeader)]
	sort.Strings(values)
	c.Assert(values, jc.DeepEquals, []string{"k1=v1", "k2=v2"})

	// Make a third request after restoring empty attrs.
	repo = repo.WithJujuAttrs(nil)
	_, err = repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(header.Get(charmrepo.JujuMetadataHTTPHeader), gc.Equals, "")
}
