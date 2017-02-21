package testing_test

import (
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/filetesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"

	"gopkg.in/juju/charmrepo.v2-unstable/testing"
)

var _ = gc.Suite(&testCharmSuite{})

type testCharmSuite struct{}

var newCharmTests = []struct {
	about          string
	spec           testing.CharmSpec
	expectMeta     *charm.Meta
	expectConfig   *charm.Config
	expectActions  *charm.Actions
	expectMetrics  *charm.Metrics
	expectFiles    filetesting.Entries
	expectRevision int
}{{
	about: "all charm populated without files",
	spec: testing.CharmSpec{
		Meta: `
name: mysql
summary: "Database engine"
description: "A pretty popular database"
provides:
  server: mysql
`,
		Config: `
options:
  blog-title: {default: My Title, description: Config description, type: string}
`,
		Actions: `
snapshot:
   description: Take a snapshot of the database.
   params:
      outfile:
         description: outfile description
         type: string
         default: foo.bz2
`,
		Metrics: `
metrics:
  pings:
    type: gauge
    description: Description of the metric.
`,
		Revision: 99,
	},
	expectMeta: &charm.Meta{
		Name:        "mysql",
		Summary:     "Database engine",
		Description: "A pretty popular database",
		Provides: map[string]charm.Relation{
			"server": {
				Name:      "server",
				Role:      charm.RoleProvider,
				Interface: "mysql",
				Scope:     charm.ScopeGlobal,
			},
		},
	},
	expectConfig: &charm.Config{
		Options: map[string]charm.Option{
			"blog-title": {
				Type:        "string",
				Description: "Config description",
				Default:     "My Title",
			},
		},
	},
	expectActions: &charm.Actions{
		ActionSpecs: map[string]charm.ActionSpec{
			"snapshot": {
				Description: "Take a snapshot of the database.",
				Params: map[string]interface{}{
					"title":       "snapshot",
					"description": "Take a snapshot of the database.",
					"type":        "object",
					"properties": map[string]interface{}{
						"outfile": map[string]interface{}{
							"description": "outfile description",
							"type":        "string",
							"default":     "foo.bz2",
						},
					},
				},
			},
		},
	},
	expectMetrics: &charm.Metrics{
		Metrics: map[string]charm.Metric{
			"pings": {
				Type:        charm.MetricTypeGauge,
				Description: "Description of the metric.",
			},
		},
	},
	expectFiles: filetesting.Entries{
		filetesting.File{
			Path: "hooks/install",
			Data: "#!/bin/sh\n",
			Perm: 0755,
		},
		filetesting.File{
			Path: "hooks/start",
			Data: "#!/bin/sh\n",
			Perm: 0755,
		},
	},
	expectRevision: 99,
}, {
	about: "charm with some extra files specified",
	spec: testing.CharmSpec{
		Meta: `
name: mycharm
summary: summary
description: description
`,
		Files: filetesting.Entries{
			filetesting.File{
				Path: "hooks/customhook",
				Data: "custom stuff",
				Perm: 0755,
			},
		},
	},
	expectMeta: &charm.Meta{
		Name:        "mycharm",
		Summary:     "summary",
		Description: "description",
	},
	expectConfig: &charm.Config{
		Options: map[string]charm.Option{},
	},
	expectActions: &charm.Actions{},
	expectFiles: filetesting.Entries{
		filetesting.File{
			Path: "hooks/customhook",
			Data: "custom stuff",
			Perm: 0755,
		},
	},
},
}

func (*testCharmSuite) TestNewCharm(c *gc.C) {
	for i, test := range newCharmTests {
		c.Logf("test %d: %s", i, test.about)
		ch := testing.NewCharm(c, test.spec)
		c.Assert(ch.Meta(), jc.DeepEquals, test.expectMeta)
		c.Assert(ch.Config(), jc.DeepEquals, test.expectConfig)
		c.Assert(ch.Metrics(), jc.DeepEquals, test.expectMetrics)
		c.Assert(ch.Actions(), jc.DeepEquals, test.expectActions)
		c.Assert(ch.Revision(), gc.Equals, test.expectRevision)

		archive := ch.Archive()
		c.Assert(archive.Meta(), jc.DeepEquals, test.expectMeta)
		c.Assert(archive.Config(), jc.DeepEquals, test.expectConfig)
		c.Assert(archive.Metrics(), jc.DeepEquals, test.expectMetrics)
		c.Assert(archive.Actions(), jc.DeepEquals, test.expectActions)
		c.Assert(archive.Revision(), gc.Equals, test.expectRevision)

		// Check that we get the same archive again.
		c.Assert(ch.Archive(), gc.Equals, archive)
		c.Assert(ch.ArchiveBytes(), gc.Not(gc.HasLen), 0)

		dir := c.MkDir()
		err := archive.ExpandTo(dir)
		c.Assert(err, gc.IsNil)
		test.expectFiles.Check(c, dir)

	}
}

func (*testCharmSuite) TestMetaWithRelations(c *gc.C) {
	m := testing.MetaWithRelations(
		nil,
		"provides foo aninterface",
		"provides bar another",
		"requires blah more",
		"requires a b",
		"provides c d",
	)
	c.Assert(m, jc.DeepEquals, &charm.Meta{
		Provides: map[string]charm.Relation{
			"foo": {
				Name:      "foo",
				Scope:     charm.ScopeGlobal,
				Interface: "aninterface",
				Role:      charm.RoleProvider,
			},
			"bar": {
				Name:      "bar",
				Scope:     charm.ScopeGlobal,
				Interface: "another",
				Role:      charm.RoleProvider,
			},
			"c": {
				Name:      "c",
				Scope:     charm.ScopeGlobal,
				Interface: "d",
				Role:      charm.RoleProvider,
			},
		},
		Requires: map[string]charm.Relation{
			"blah": {
				Name:      "blah",
				Scope:     charm.ScopeGlobal,
				Interface: "more",
				Role:      charm.RoleRequirer,
			},
			"a": {
				Name:      "a",
				Scope:     charm.ScopeGlobal,
				Interface: "b",
				Role:      charm.RoleRequirer,
			},
		},
	})
}

func (*testCharmSuite) TestMetaWithResources(c *gc.C) {
	m := testing.MetaWithResources(nil, "one", "two")
	c.Assert(m, jc.DeepEquals, &charm.Meta{
		Resources: map[string]resource.Meta{
			"one": {
				Name:        "one",
				Type:        resource.TypeFile,
				Path:        "one-file",
				Description: "one description",
			},
			"two": {
				Name:        "two",
				Type:        resource.TypeFile,
				Path:        "two-file",
				Description: "two description",
			},
		},
	})
}
