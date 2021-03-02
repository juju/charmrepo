module github.com/juju/charmrepo/v5

go 1.14

require (
	github.com/go-macaroon-bakery/macaroon-bakery/v3 v3.0.0-20210302105820-5587392d1f47
	github.com/juju/charm/v7 v7.0.0-20200625165032-ef717232a815
	github.com/juju/errors v0.0.0-20200330140219-3fe23663418f
	github.com/juju/loggo v0.0.0-20200526014432-9ce3a2e09b5e
	github.com/juju/testing v0.0.0-20191001232224-ce9dec17d28b
	github.com/juju/utils v0.0.0-20200116185830-d40c2fe10647
	golang.org/x/net v0.0.0-20210226172049-e18ecbb05110
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c
	gopkg.in/errgo.v1 v1.0.1
	gopkg.in/httprequest.v1 v1.2.0
	gopkg.in/juju/charmstore.v5 v5.7.1
	gopkg.in/juju/idmclient.v1 v1.0.0-20180320161856-203d20774ce8
	gopkg.in/macaroon-bakery.v2 v2.2.0 // indirect
	gopkg.in/macaroon-bakery.v2-unstable v2.0.0-20160623142747-5a131df02b23
	gopkg.in/macaroon.v2 v2.1.0
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	gopkg.in/yaml.v2 v2.4.0
)

replace github.com/juju/utils => github.com/juju/utils v0.0.0-20180619112806-c746c6e86f4f
