// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient_test

import (
	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmrepo/v7/csclient"
)

type suite struct {
	jujutesting.IsolationSuite
}

var _ = gc.Suite(&suite{})

var hyphenateTests = []struct {
	val    string
	expect string
}{{
	val:    "Hello",
	expect: "hello",
}, {
	val:    "HelloThere",
	expect: "hello-there",
}, {
	val:    "HelloHTTP",
	expect: "hello-http",
}, {
	val:    "helloHTTP",
	expect: "hello-http",
}, {
	val:    "hellothere",
	expect: "hellothere",
}, {
	val:    "Long4Camel32WithDigits45",
	expect: "long4-camel32-with-digits45",
}, {
	// The result here is equally dubious, but Go identifiers
	// should not contain underscores.
	val:    "With_Dubious_Underscore",
	expect: "with_-dubious_-underscore",
}}

func (s *suite) TestHyphenate(c *gc.C) {
	for i, test := range hyphenateTests {
		c.Logf("test %d. %q", i, test.val)
		c.Assert(csclient.Hyphenate(test.val), gc.Equals, test.expect)
	}
}
