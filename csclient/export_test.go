// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient // import "gopkg.in/juju/charmrepo.v3/csclient"

var (
	Hyphenate              = hyphenate
	MinMultipartUploadSize = &minMultipartUploadSize
	UploadArchive          = (*Client).uploadArchive
)
