module github.com/juju/charmrepo/v7

go 1.14

require (
	github.com/juju/charm/v9 v9.0.0-20210125110411-23fabd67cb4c
	github.com/juju/errors v0.0.0-20200330140219-3fe23663418f
	github.com/juju/loggo v0.0.0-20200526014432-9ce3a2e09b5e
	github.com/juju/os v0.0.0-20191022170002-da411304426c // indirect
	github.com/juju/testing v0.0.0-20200923013621-75df6121fbb0
	github.com/juju/utils v0.0.0-20200116185830-d40c2fe10647
	github.com/kr/pretty v0.2.1 // indirect
	golang.org/x/net v0.0.0-20200904194848-62affa334b73
	gopkg.in/check.v1 v1.0.0-20200902074654-038fdea0a05b
	gopkg.in/errgo.v1 v1.0.0
	gopkg.in/httprequest.v1 v1.1.1
	gopkg.in/juju/charmstore.v5 v5.7.1
	gopkg.in/juju/idmclient.v1 v1.0.0-20180320161856-203d20774ce8
	gopkg.in/juju/names.v2 v2.0.0-20190813004204-e057c73bd1be // indirect
	gopkg.in/macaroon-bakery.v2 v2.0.0-20180423133735-a0743b6619d6
	gopkg.in/macaroon-bakery.v2-unstable v2.0.0-20160623142747-5a131df02b23
	gopkg.in/macaroon.v2 v2.0.0
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	gopkg.in/yaml.v2 v2.3.0
)

replace github.com/juju/utils => github.com/juju/utils v0.0.0-20180619112806-c746c6e86f4f
