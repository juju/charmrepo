module github.com/juju/charmrepo/v5

go 1.14

require (
	github.com/juju/charm/v7 v7.0.0-20200424215011-2c5875af8596
	github.com/juju/clock v0.0.0-20190205081909-9c5c9712527c // indirect
	github.com/juju/errors v0.0.0-20190930114154-d42613fe1ab9 // indirect
	github.com/juju/loggo v0.0.0-20190526231331-6e530bcce5d8
	github.com/juju/os v0.0.0-20191022170002-da411304426c // indirect
	github.com/juju/testing v0.0.0-20191001232224-ce9dec17d28b
	github.com/juju/utils v0.0.0-20180820210520-bf9cc5bdd62d
	github.com/juju/version v0.0.0-20191106052214-a0f5311c2166 // indirect
	golang.org/x/crypto v0.0.0-20191206172530-e9b2fee46413 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/sys v0.0.0-20191210023423-ac6580df4449 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15
	gopkg.in/errgo.v1 v1.0.0
	gopkg.in/httprequest.v1 v1.0.0-20180319125457-3531529dedf0
	gopkg.in/juju/charmstore.v5 v5.7.1
	gopkg.in/juju/idmclient.v1 v1.0.0-20180320161856-203d20774ce8
	gopkg.in/juju/names.v3 v3.0.0-20191210002836-39289f373765 // indirect
	gopkg.in/macaroon-bakery.v2 v2.0.0-20180423133735-a0743b6619d6
	gopkg.in/macaroon-bakery.v2-unstable v2.0.0-20160623142747-5a131df02b23
	gopkg.in/macaroon.v2 v2.0.0
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	gopkg.in/yaml.v2 v2.2.7
)

replace github.com/juju/utils => github.com/juju/utils v0.0.0-20180619112806-c746c6e86f4f
