// Copyright 2017 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package iomon_test

import (
	"time"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"

	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charmrepo.v2-unstable/internal/iomon"
)

type iomonSuite struct{}

var _ = gc.Suite(&iomonSuite{})

func (*iomonSuite) TestMonitor(c *gc.C) {
	setterCh := make(statusSetter)
	t0 := time.Now()
	clock := testing.NewClock(t0)
	m := iomon.New(iomon.Params{
		Size:           1000,
		Setter:         setterCh,
		UpdateInterval: time.Second,
		Clock:          clock,
	})
	c.Assert(setterCh.wait(c), jc.DeepEquals, iomon.Status{
		Current: 0,
		Total:   1000,
	})
	clock.Advance(time.Second)
	// Nothing changed, so no status should be sent.
	setterCh.expectNothing(c)
	// Calling update should not trigger a status send.
	m.Update(500)
	setterCh.expectNothing(c)
	clock.Advance(time.Second)
	c.Assert(setterCh.wait(c), jc.DeepEquals, iomon.Status{
		Current: 500,
		Total:   1000,
	})
	m.Update(700)
	m.Kill()
	// One last status update should be sent when it's killed.
	c.Assert(setterCh.wait(c), jc.DeepEquals, iomon.Status{
		Current: 700,
		Total:   1000,
	})
	m.Wait()
	clock.Advance(10 * time.Second)
	setterCh.expectNothing(c)
}

var formatByteCountTests = []struct {
	n      int64
	expect string
}{
	{0, "0KiB"},
	{2567, "3KiB"},
	{9876 * 1024, "9876KiB"},
	{10 * 1024 * 1024, "10.0MiB"},
	{20 * 1024 * 1024 * 1024, "20.0GiB"},
	{55068359375, "51.3GiB"},
}

func (*iomonSuite) TestFormatByteCount(c *gc.C) {
	for i, test := range formatByteCountTests {
		c.Logf("test %d: %v", i, test.n)
		c.Assert(iomon.FormatByteCount(test.n), gc.Equals, test.expect)
	}
}

type statusSetter chan iomon.Status

func (ch statusSetter) wait(c *gc.C) iomon.Status {
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		c.Fatalf("timed out waiting for status")
		panic("unreachable")
	}
}

func (ch statusSetter) expectNothing(c *gc.C) {
	select {
	case s := <-ch:
		c.Fatalf("unexpected status received %#v", s)
	case <-time.After(10 * time.Millisecond):
	}
}

func (ch statusSetter) SetStatus(s iomon.Status) {
	ch <- s
}
