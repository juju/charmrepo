module github.com/juju/charmrepo/v5

go 1.14

require (
	github.com/juju/charm/v8 v8.0.0-20200817113526-2a88e9b46b47
	github.com/juju/errors v0.0.0-20190930114154-d42613fe1ab9
	github.com/juju/loggo v0.0.0-20190526231331-6e530bcce5d8
	github.com/juju/testing v0.0.0-20191001232224-ce9dec17d28b
	github.com/juju/utils v0.0.0-20180820210520-bf9cc5bdd62d
	golang.org/x/net v0.0.0-20200813134508-3edf25e44fcc
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15
	gopkg.in/errgo.v1 v1.0.0
	gopkg.in/httprequest.v1 v1.0.0-20180319125457-3531529dedf0
	gopkg.in/juju/charmstore.v5 v5.7.1
	gopkg.in/juju/idmclient.v1 v1.0.0-20180320161856-203d20774ce8
	gopkg.in/macaroon-bakery.v2 v2.0.0-20180423133735-a0743b6619d6
	gopkg.in/macaroon-bakery.v2-unstable v2.0.0-20160623142747-5a131df02b23
	gopkg.in/macaroon.v2 v2.0.0
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	gopkg.in/yaml.v2 v2.2.7
)

replace github.com/juju/utils => github.com/juju/utils v0.0.0-20180619112806-c746c6e86f4f
