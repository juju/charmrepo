// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo // import "github.com/juju/charmrepo/v6"

import (
	"crypto/sha512"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/juju/charm/v8"
	"gopkg.in/errgo.v1"

	"github.com/juju/charmrepo/v6/csclient"
	"github.com/juju/charmrepo/v6/csclient/params"
)

// CharmStore is a repository Interface that provides access to the public Juju
// charm store.
type CharmStore struct {
	client *csclient.Client
}

var _ Interface = (*CharmStore)(nil)

// NewCharmStoreParams holds parameters for instantiating a new CharmStore.
type NewCharmStoreParams struct {
	// URL holds the root endpoint URL of the charm store,
	// with no trailing slash, not including the version.
	// For example https://api.jujucharms.com/charmstore
	// If empty, the default charm store client location is used.
	URL string

	// BakeryClient holds the bakery client to use when making
	// requests to the store. This is used in preference to
	// HTTPClient.
	BakeryClient *httpbakery.Client

	// User holds the name to authenticate as for the client. If User is empty,
	// no credentials will be sent.
	User string

	// Password holds the password for the given user, for authenticating the
	// client.
	Password string
}

// NewCharmStore creates and returns a charm store repository.
// The given parameters are used to instantiate the charm store.
//
// The errors returned from the interface methods will
// preserve the causes returned from the underlying csclient
// methods.
func NewCharmStore(p NewCharmStoreParams) *CharmStore {
	client := csclient.New(csclient.Params{
		URL:          p.URL,
		BakeryClient: p.BakeryClient,
		User:         p.User,
		Password:     p.Password,
	})
	return NewCharmStoreFromClient(client)
}

// NewCharmStoreFromClient creates and returns a charm store repository.
// The provided client is used for charm store requests.
func NewCharmStoreFromClient(client *csclient.Client) *CharmStore {
	return &CharmStore{
		client: client,
	}
}

// Client returns the charmstore client that the CharmStore
// implementation uses under the hood.
func (s *CharmStore) Client() *csclient.Client {
	return s.client
}

// Get implements Interface.Get.
func (s *CharmStore) Get(curl *charm.URL, archivePath string) (*charm.CharmArchive, error) {
	if curl.Series == "bundle" {
		return nil, errgo.Newf("expected a charm URL, got bundle URL %q", curl)
	}
	f, err := os.Create(archivePath)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	defer f.Close()
	if err := s.getArchive(curl, f); err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return charm.ReadCharmArchive(archivePath)
}

// GetBundle implements Interface.GetBundle.
func (s *CharmStore) GetBundle(curl *charm.URL, archivePath string) (charm.Bundle, error) {
	if curl.Series != "bundle" {
		return nil, errgo.Newf("expected a bundle URL, got charm URL %q", curl)
	}
	f, err := os.Create(archivePath)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	defer f.Close()
	if err := s.getArchive(curl, f); err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return charm.ReadBundleArchive(archivePath)
}

// getArchive reads the archive from the given charm or bundle URL
// and writes it to the given writer.
func (s *CharmStore) getArchive(curl *charm.URL, w io.Writer) error {
	etype := "charm"
	if curl.Series == "bundle" {
		etype = "bundle"
	}
	r, _, expectHash, expectSize, err := s.client.GetArchive(curl)
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			// Make a prettier error message for the user.
			return errgo.WithCausef(nil, params.ErrNotFound, "cannot retrieve %q: %s not found", curl, etype)
		}
		return errgo.NoteMask(err, fmt.Sprintf("cannot retrieve %s %q", etype, curl), errgo.Any)
	}
	defer r.Close()

	hash := sha512.New384()
	size, err := io.Copy(io.MultiWriter(hash, w), r)
	if err != nil {
		return errgo.Notef(err, "cannot read entity archive")
	}
	if size != expectSize {
		return errgo.Newf("size mismatch; network corruption?")
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != expectHash {
		return errgo.Newf("hash mismatch; network corruption?")
	}
	return nil
}

// Resolve implements Interface.Resolve.
func (s *CharmStore) Resolve(ref *charm.URL) (*charm.URL, []string, error) {
	resolved, _, supportedSeries, err := s.ResolveWithChannel(ref)
	if err != nil {
		return nil, nil, errgo.Mask(err, errgo.Any)
	}
	return resolved, supportedSeries, nil
}

// ResolveWithChannel does the same thing as Resolve() but also returns
// the best channel to use.
func (s *CharmStore) ResolveWithChannel(ref *charm.URL) (*charm.URL, params.Channel, []string, error) {
	return s.ResolveWithPreferredChannel(ref, s.client.Channel())
}

// ResolveWithPreferredChannel does the same thing as ResolveWithChannel() but
// allows callers to specify a preferred channel to use.
func (s *CharmStore) ResolveWithPreferredChannel(ref *charm.URL, channel params.Channel) (*charm.URL, params.Channel, []string, error) {
	var result struct {
		Id              params.IdResponse
		SupportedSeries params.SupportedSeriesResponse
		Published       params.PublishedResponse
	}

	if _, err := s.client.MetaWithChannel(ref, &result, channel); err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			// Make a prettier error message for the user.
			etype := "charm"
			switch ref.Series {
			case "bundle":
				etype = "bundle"
			case "":
				etype = "charm or bundle"
			}
			return nil, params.NoChannel, nil, errgo.WithCausef(nil, params.ErrNotFound, "cannot resolve URL %q: %s not found", ref, etype)
		}
		return nil, params.NoChannel, nil, errgo.NoteMask(err, fmt.Sprintf("cannot resolve charm URL %q", ref), errgo.Any)
	}

	// If no preferredChannel is specified then we should use the (optional)
	// csclient channel value as our preferredChannel.
	if channel == params.NoChannel {
		channel = s.client.Channel()
	}

	// TODO(ericsnow) Get this directly from the API. It has high risk
	// of getting stale. Perhaps add params.PublishedResponse.BestChannel
	// or, less desireably, have params.PublishedResponse.Info be
	// priority-ordered.
	channel = bestChannel(s.client, result.Published.Info, channel)
	return result.Id.Id, channel, result.SupportedSeries.SupportedSeries, nil
}

// GetFileFromArchive streams the contents of the requested filename from the
// given charm or bundle archive, returning a reader its data can be read from.
func (s *CharmStore) GetFileFromArchive(charmURL *charm.URL, filename string) (io.ReadCloser, error) {
	r, err := s.client.GetFileFromArchive(charmURL, filename)
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			return nil, params.ErrNotFound
		}
		return nil, err
	}

	return r, err
}

// Meta fetches metadata on the charm or bundle with the
// given id. The result value provides a value
// to be filled in with the result, which must be
// a pointer to a struct containing members corresponding
// to possible metadata include parameters
// (see https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmeta).
//
// It returns the fully qualified id of the entity.
//
// The name of the struct member is translated to
// a lower case hyphen-separated form; for example,
// ArchiveSize becomes "archive-size", and BundleMachineCount
// becomes "bundle-machine-count", but may also
// be specified in the field's tag
//
// This example will fill in the result structure with information
// about the given id, including information on its archive
// size (include archive-size), upload time (include archive-upload-time)
// and digest (include extra-info/digest).
//
//	var result struct {
//		ArchiveSize params.ArchiveSizeResponse
//		ArchiveUploadTime params.ArchiveUploadTimeResponse
//		Digest string `csclient:"extra-info/digest"`
//	}
//	id, err := client.Meta(id, &result)
func (s *CharmStore) Meta(charmURL *charm.URL, result interface{}) (*charm.URL, error) {
	return s.client.Meta(charmURL, result)
}

// bestChannel determines the best channel to use for the given client
// and published info.
//
// Note that this is equivalent to code on the server side.
// See ReqHandler.entityChannel in internal/v5/auth.go.
func bestChannel(client *csclient.Client, published []params.PublishedInfo, preferredChannel params.Channel) params.Channel {
	if preferredChannel != params.NoChannel {
		return preferredChannel
	}
	if len(published) == 0 {
		return params.UnpublishedChannel
	}

	// Note the the meta/published endpoint returns results in stability level
	// order. For instance, the stable channel comes first, then candidate etc.
	// TODO frankban: that said, while the old charm store is being used, we
	// still need to sort them. Later, we will be able to just
	// "return published[0].Channel" here.
	// TODO(ericsnow) Favor the one with info.Current == true?
	channels := make([]params.Channel, len(published))
	for i, result := range published {
		channels[i] = result.Channel
	}
	sortChannels(channels)
	return channels[0]
}

// oldChannels maps old charm store channels with their stability level.
var oldChannels = map[params.Channel]int{
	params.StableChannel:      1,
	params.DevelopmentChannel: 2,
	params.UnpublishedChannel: 3,
}

// sortChannels sorts the given channels by stability level, most stable first.
func sortChannels(channels []params.Channel) {
	for _, channel := range channels {
		if _, ok := oldChannels[channel]; !ok {
			return
		}
	}
	// All channels are old: sort in legacy order.
	sort.Sort(orderedOldChannels(channels))
}

type orderedOldChannels []params.Channel

func (o orderedOldChannels) Len() int           { return len(o) }
func (o orderedOldChannels) Swap(i, j int)      { o[i], o[j] = o[j], o[i] }
func (o orderedOldChannels) Less(i, j int) bool { return oldChannels[o[i]] < oldChannels[o[j]] }
