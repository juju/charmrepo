// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetests

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juju/charm/v8"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charmstore.v5"

	"github.com/juju/charmrepo/v6"
	"github.com/juju/charmrepo/v6/csclient"
	"github.com/juju/charmrepo/v6/csclient/params"
	charmtesting "github.com/juju/charmrepo/v6/testing"
)

type charmStoreSuite struct {
	jujutesting.IsolationSuite
}

var TestCharms = charmtesting.NewRepo("internal/test-charm-repo", "quantal")

var _ = gc.Suite(&charmStoreSuite{})

func (s *charmStoreSuite) TestDefaultURL(c *gc.C) {
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{})
	c.Assert(repo.Client().ServerURL(), gc.Equals, csclient.ServerURL)
}

type charmStoreBaseSuite struct {
	charmtesting.IsolatedMgoSuite
	srv     *httptest.Server
	client  *csclient.Client
	handler charmstore.HTTPCloseHandler
	repo    *charmrepo.CharmStore
}

var _ = gc.Suite(&charmStoreBaseSuite{})

func (s *charmStoreBaseSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.startServer(c)
	s.repo = charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: s.srv.URL,
	})
}

func (s *charmStoreBaseSuite) TearDownTest(c *gc.C) {
	s.srv.Close()
	s.handler.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *charmStoreBaseSuite) startServer(c *gc.C) {
	serverParams := charmstore.ServerParams{
		AuthUsername: "test-user",
		AuthPassword: "test-password",
	}

	db := s.Session.DB("charmstore")
	handler, err := charmstore.NewServer(db, nil, "", serverParams, charmstore.V5)
	c.Assert(err, gc.IsNil)
	s.handler = handler
	s.srv = httptest.NewServer(handler)
	s.client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
	})
}

// addCharm uploads a charm a promulgated revision to the testing charm store,
// and returns the resulting charm and charm URL.
func (s *charmStoreBaseSuite) addCharm(c *gc.C, urlStr, name string) (charm.Charm, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	promulgatedRevision := -1
	if id.User == "" {
		id.User = "who"
		promulgatedRevision = id.Revision
	}
	ch := TestCharms.CharmArchive(c.MkDir(), name)

	// Upload the charm.
	err := s.client.UploadCharmWithRevision(id, ch, promulgatedRevision)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id, params.StableChannel)
	return ch, id
}

// addCharmNoRevision uploads a charm to the testing charm store, and returns the
// resulting charm and charm URL.
func (s *charmStoreBaseSuite) addCharmNoRevision(c *gc.C, urlStr, name string) (charm.Charm, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	if id.User == "" {
		id.User = "who"
	}
	ch := TestCharms.CharmArchive(c.MkDir(), name)

	// Upload the charm.
	url, err := s.client.UploadCharm(id, ch)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id, params.StableChannel)

	return ch, url
}

// addBundle uploads a bundle to the testing charm store, and returns the
// resulting bundle and bundle URL.
func (s *charmStoreBaseSuite) addBundle(c *gc.C, urlStr, name string) (charm.Bundle, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	promulgatedRevision := -1
	if id.User == "" {
		id.User = "who"
		promulgatedRevision = id.Revision
	}
	b := TestCharms.BundleArchive(c.MkDir(), name)

	// Upload the bundle.
	err := s.client.UploadBundleWithRevision(id, b, promulgatedRevision)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id, params.StableChannel)

	// Return the bundle and its URL.
	return b, id
}

func (s *charmStoreBaseSuite) setPublic(c *gc.C, id *charm.URL, channels ...params.Channel) {
	if len(channels) > 0 {
		err := s.client.WithChannel(params.UnpublishedChannel).Put("/"+id.Path()+"/publish", &params.PublishRequest{
			Channels: channels,
		})
		c.Assert(err, jc.ErrorIsNil)
	} else {
		channels = []params.Channel{params.UnpublishedChannel}
	}

	for _, channel := range channels {
		// Allow read permissions to everyone.
		err := s.client.WithChannel(channel).Put("/"+id.Path()+"/meta/perm/read", []string{params.Everyone})
		c.Assert(err, jc.ErrorIsNil)
	}
}

type charmStoreRepoSuite struct {
	charmStoreBaseSuite
}

var _ = gc.Suite(&charmStoreRepoSuite{})

// checkCharmDownloads checks that the charm represented by the given URL has
// been downloaded the expected number of times.
func (s *charmStoreRepoSuite) checkCharmDownloads(c *gc.C, url *charm.URL, expect int) {
	key := []string{params.StatsArchiveDownload, url.Series, url.Name, url.User, strconv.Itoa(url.Revision)}
	path := "/stats/counter/" + strings.Join(key, ":")
	var count int

	getDownloads := func() int {
		var result []params.Statistic
		err := s.client.Get(path, &result)
		c.Assert(err, jc.ErrorIsNil)
		return int(result[0].Count)
	}

	for retry := 0; retry < 10; retry++ {
		time.Sleep(100 * time.Millisecond)
		if count = getDownloads(); count == expect {
			if expect == 0 && retry < 2 {
				// Wait a bit to make sure.
				continue
			}
			return
		}
	}
	c.Errorf("downloads count for %s is %d, expected %d", url, count, expect)
}

func (s *charmStoreRepoSuite) TestNewCharmStoreFromClient(c *gc.C) {
	client := csclient.New(csclient.Params{URL: csclient.ServerURL})

	repo := charmrepo.NewCharmStoreFromClient(client)

	c.Check(repo.Client().ServerURL(), gc.Equals, csclient.ServerURL)
}

func (s *charmStoreRepoSuite) TestGet(c *gc.C) {
	expect, url := s.addCharm(c, "cs:~who/trusty/mysql-0", "mysql")
	ch, err := s.repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect)
}

func (s *charmStoreRepoSuite) TestGetPromulgated(c *gc.C) {
	expect, url := s.addCharm(c, "cs:trusty/mysql-42", "mysql")
	ch, err := s.repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect)
}

func (s *charmStoreRepoSuite) TestGetRevisions(c *gc.C) {
	s.addCharm(c, "cs:~dalek/trusty/riak-0", "riak")
	expect1, url1 := s.addCharm(c, "cs:~dalek/trusty/riak-1", "riak")
	expect2, _ := s.addCharm(c, "cs:~dalek/trusty/riak-2", "riak")

	// Retrieve an old revision.
	ch, err := s.repo.Get(url1, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect1)

	// Retrieve the latest revision.
	ch, err = s.repo.Get(charm.MustParseURL("cs:~dalek/trusty/riak"), filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect2)
}

func (s *charmStoreRepoSuite) TestGetCache(c *gc.C) {
	_, url := s.addCharm(c, "cs:~who/trusty/mysql-42", "mysql")
	ch, err := s.repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(hashOfPath(c, ch.Path), gc.Equals, hashOfCharm(c, "mysql"))
}

func (s *charmStoreRepoSuite) TestGetIncreaseStats(c *gc.C) {
	if jujutesting.MgoServer.WithoutV8 {
		c.Skip("mongo javascript not enabled")
	}
	_, url := s.addCharm(c, "cs:~who/precise/wordpress-2", "wordpress")

	// Retrieve the charm.
	_, err := s.repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	s.checkCharmDownloads(c, url, 1)

	// Retrieve the charm again.
	_, err = s.repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, jc.ErrorIsNil)
	s.checkCharmDownloads(c, url, 2)
}

func (s *charmStoreRepoSuite) TestGetErrorBundle(c *gc.C) {
	ch, err := s.repo.Get(charm.MustParseURL("cs:bundle/django"), filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, gc.ErrorMatches, `expected a charm URL, got bundle URL "cs:bundle/django"`)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorCharmNotFound(c *gc.C) {
	ch, err := s.repo.Get(charm.MustParseURL("cs:trusty/no-such"), filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, gc.ErrorMatches, `cannot retrieve "cs:trusty/no-such": charm not found`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorServer(c *gc.C) {
	// Set up a server always returning errors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"Message": "bad wolf", "Code": "bad request"}`))
	}))
	defer srv.Close()

	// Try getting a charm from the server.
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: srv.URL,
	})
	ch, err := repo.Get(charm.MustParseURL("cs:trusty/django"), filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, gc.ErrorMatches, `cannot retrieve charm "cs:trusty/django": cannot get archive: bad wolf`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrBadRequest)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorHashMismatch(c *gc.C) {
	_, url := s.addCharm(c, "cs:trusty/riak-0", "riak")

	// Set up a proxy server that modifies the returned hash.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, r)
		w.Header().Set(params.EntityIdHeader, rec.Header().Get(params.EntityIdHeader))
		w.Header().Set(params.ContentHashHeader, "invalid")
		w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()

	// Try getting a charm from the server.
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: srv.URL,
	})
	ch, err := repo.Get(url, filepath.Join(c.MkDir(), "charm"))
	c.Assert(err, gc.ErrorMatches, `hash mismatch; network corruption\?`)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetBundle(c *gc.C) {
	// Note that getting a bundle shares most of the logic with charm
	// retrieval. For this reason, only bundle specific code is tested.
	s.addCharm(c, "cs:trusty/mysql-0", "mysql")
	s.addCharm(c, "cs:trusty/wordpress-0", "wordpress")
	expect, url := s.addBundle(c, "cs:~who/bundle/wordpress-simple-42", "wordpress-simple")
	b, err := s.repo.GetBundle(url, filepath.Join(c.MkDir(), "bundle"))
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, expect.Data())
	c.Assert(b.ReadMe(), gc.Equals, expect.ReadMe())
}

func (s *charmStoreRepoSuite) TestGetBundleErrorCharm(c *gc.C) {
	ch, err := s.repo.GetBundle(charm.MustParseURL("cs:trusty/django"), filepath.Join(c.MkDir(), "bundle"))
	c.Assert(err, gc.ErrorMatches, `expected a bundle URL, got charm URL "cs:trusty/django"`)
	c.Assert(ch, gc.IsNil)
}

var resolveTests = []struct {
	id              string
	url             string
	supportedSeries []string
	err             string
}{{
	id:              "cs:~who/mysql",
	url:             "cs:~who/trusty/mysql-0",
	supportedSeries: []string{"trusty"},
}, {
	id:              "cs:~who/trusty/mysql",
	url:             "cs:~who/trusty/mysql-0",
	supportedSeries: []string{"trusty"},
}, {
	id:              "cs:~who/wordpress",
	url:             "cs:~who/precise/wordpress-2",
	supportedSeries: []string{"precise"},
}, {
	id:  "cs:~who/wordpress-2",
	err: `cannot resolve URL "cs:~who/wordpress-2": charm or bundle not found`,
}, {
	id:              "cs:~dalek/riak",
	url:             "cs:~dalek/utopic/riak-42",
	supportedSeries: []string{"utopic"},
}, {
	id:              "cs:~dalek/utopic/riak-42",
	url:             "cs:~dalek/utopic/riak-42",
	supportedSeries: []string{"utopic"},
}, {
	id:              "cs:utopic/mysql",
	url:             "cs:utopic/mysql-47",
	supportedSeries: []string{"utopic"},
}, {
	id:              "cs:utopic/mysql-47",
	url:             "cs:utopic/mysql-47",
	supportedSeries: []string{"utopic"},
}, {
	id:              "cs:~who/multi-series",
	url:             "cs:~who/multi-series-0",
	supportedSeries: []string{"trusty", "precise", "quantal"},
}, {
	id:  "cs:~dalek/utopic/riak-100",
	err: `cannot resolve URL "cs:~dalek/utopic/riak-100": charm not found`,
}, {
	id:  "cs:bundle/no-such",
	err: `cannot resolve URL "cs:bundle/no-such": bundle not found`,
}, {
	id:  "cs:no-such",
	err: `cannot resolve URL "cs:no-such": charm or bundle not found`,
}}

func (s *charmStoreRepoSuite) addResolveTestsCharms(c *gc.C) {
	// Add promulgated entities first so that the base entity
	// is marked as promulgated when it first gets inserted.
	s.addCharm(c, "cs:utopic/mysql-47", "mysql")
	s.addCharmNoRevision(c, "cs:multi-series", "multi-series")

	s.addCharm(c, "cs:~who/trusty/mysql-0", "mysql")
	s.addCharm(c, "cs:~who/precise/wordpress-2", "wordpress")
	s.addCharm(c, "cs:~dalek/utopic/riak-42", "riak")
}

func (s *charmStoreRepoSuite) TestResolve(c *gc.C) {
	s.addResolveTestsCharms(c)
	client := s.repo.Client().WithChannel(params.StableChannel)
	repo := charmrepo.NewCharmStoreFromClient(client)
	for i, test := range resolveTests {
		c.Logf("test %d: %s", i, test.id)
		ref, supportedSeries, err := repo.Resolve(charm.MustParseURL(test.id))
		if test.err != "" {
			c.Check(err.Error(), gc.Equals, test.err)
			c.Check(ref, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		c.Check(ref, jc.DeepEquals, charm.MustParseURL(test.url))
		c.Check(supportedSeries, jc.SameContents, test.supportedSeries)
	}
}

func (s *charmStoreRepoSuite) TestResolveWithChannelEquivalentToResolve(c *gc.C) {
	s.addResolveTestsCharms(c)
	client := s.repo.Client().WithChannel(params.StableChannel)
	repo := charmrepo.NewCharmStoreFromClient(client)
	for i, test := range resolveTests {
		c.Logf("test %d: %s", i, test.id)
		ref, channel, supportedSeries, err := repo.ResolveWithChannel(charm.MustParseURL(test.id))
		if test.err != "" {
			c.Check(err.Error(), gc.Equals, test.err)
			c.Check(ref, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		c.Check(ref, jc.DeepEquals, charm.MustParseURL(test.url))
		c.Check(channel, gc.Equals, params.StableChannel)
		c.Check(supportedSeries, jc.SameContents, test.supportedSeries)
	}
}

var channelResolveTests = []struct {
	clientChannel params.Channel
	published     []params.Channel
	expected      params.Channel
}{{
	clientChannel: params.StableChannel,
	expected:      params.StableChannel,
}, {
	clientChannel: params.EdgeChannel,
	expected:      params.EdgeChannel,
}, {
	clientChannel: params.UnpublishedChannel,
	expected:      params.UnpublishedChannel,
}, {
	clientChannel: params.NoChannel,
	expected:      params.UnpublishedChannel,
}, {
	published: []params.Channel{params.StableChannel},
	expected:  params.StableChannel,
}, {
	published: []params.Channel{params.EdgeChannel},
	expected:  params.EdgeChannel,
}, {
	published: []params.Channel{params.StableChannel, params.EdgeChannel},
	expected:  params.StableChannel,
}, {
	published: []params.Channel{params.EdgeChannel, params.StableChannel},
	expected:  params.StableChannel,
}, {
	published: []params.Channel{params.EdgeChannel, params.BetaChannel, params.CandidateChannel},
	expected:  params.CandidateChannel,
}, {
	clientChannel: params.StableChannel,
	published:     []params.Channel{params.EdgeChannel, params.StableChannel},
	expected:      params.StableChannel,
}, {
	clientChannel: params.EdgeChannel,
	published:     []params.Channel{params.StableChannel, params.EdgeChannel},
	expected:      params.EdgeChannel,
}, {
	clientChannel: params.UnpublishedChannel,
	published:     []params.Channel{params.StableChannel},
	expected:      params.UnpublishedChannel,
}, {
	clientChannel: params.CandidateChannel,
	published:     []params.Channel{params.EdgeChannel, params.CandidateChannel, params.StableChannel},
	expected:      params.CandidateChannel,
}, {
	expected: params.UnpublishedChannel,
}}

func (s *charmStoreRepoSuite) TestResolveWithGloballyForcedChannel(c *gc.C) {
	ch := TestCharms.CharmArchive(c.MkDir(), "mysql")
	cURL := charm.MustParseURL("cs:~who/trusty/mysql")

	for i, test := range channelResolveTests {
		c.Logf("test %d: %s/%v", i, test.clientChannel, test.published)

		cURL.Revision = i
		err := s.client.UploadCharmWithRevision(cURL, ch, cURL.Revision)
		c.Assert(err, gc.IsNil)
		s.setPublic(c, cURL)
		if len(test.published) > 0 {
			s.setPublic(c, cURL, test.published...)
		} else if test.clientChannel != params.NoChannel && test.clientChannel != params.UnpublishedChannel {
			s.setPublic(c, cURL, test.clientChannel)
		}
		repo := charmrepo.NewCharmStoreFromClient(s.client.WithChannel(test.clientChannel))

		_, channel, _, err := repo.ResolveWithChannel(cURL)
		c.Assert(err, jc.ErrorIsNil)

		c.Check(channel, gc.Equals, test.expected)
	}
}

func (s *charmStoreRepoSuite) TestResolveWithPreferredChannel(c *gc.C) {
	ch := TestCharms.CharmArchive(c.MkDir(), "mysql")
	cURL := charm.MustParseURL("cs:~who/trusty/mysql")

	for i, test := range channelResolveTests {
		c.Logf("test %d: %s/%v", i, test.clientChannel, test.published)

		cURL.Revision = i
		err := s.client.UploadCharmWithRevision(cURL, ch, cURL.Revision)
		c.Assert(err, gc.IsNil)
		s.setPublic(c, cURL)
		if len(test.published) > 0 {
			s.setPublic(c, cURL, test.published...)
		} else if test.clientChannel != params.NoChannel && test.clientChannel != params.UnpublishedChannel {
			s.setPublic(c, cURL, test.clientChannel)
		}

		// Instead of forcing a particular channel to the client, pass
		// it as the preferred channel to the URL resolver.
		repo := charmrepo.NewCharmStoreFromClient(s.client)
		_, channel, _, err := repo.ResolveWithPreferredChannel(cURL, test.clientChannel)
		c.Assert(err, jc.ErrorIsNil)

		c.Check(channel, gc.Equals, test.expected)
	}
}

// checkCharm checks that the given charms have the same attributes.
func checkCharm(c *gc.C, ch, expect charm.Charm) {
	c.Assert(ch.Actions(), jc.DeepEquals, expect.Actions())
	c.Assert(ch.Config(), jc.DeepEquals, expect.Config())
	c.Assert(ch.Meta(), jc.DeepEquals, expect.Meta())
}
