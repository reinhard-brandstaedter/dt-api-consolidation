## Dynatrace API Consolidation
The intention of this project is to simplify the access to multiple Dynatrace API endpoints across a large scale deployment of Dynatrace Managed. I've created this project to manage the configuration of a few thousand Dynatrace Managed tenants but soon found this consolidated API to be useful for other usecases as well. Today I'm using the consolidated API for:
- quick&easy access to the problem API for many tenants and single visualization dashboard
- configuration management across many tenants
- parameterized ad-hoc API access
- data export

Usually you would need to maintain API tokens and access permissions for every Dynatrace tenant/environment manually. You'd create API tokens and make sure they are rotated over time for security reasons, you would keep them somewhere and use them in your automation.
I've created this consolidated API to remove that burden and to also speed up any API queries/posts that would just take to long if you would do them in an iterative way in your code or even manually). For example to push a single config entity to thousand Dynatrace tenants you would eventually just iterate through every tenant and exec the API call. With this consolidated API you would only create one request and it takes care of parallelizing the call with the right auth tokens to many tenants much faster. 

### How it works
The consolidated API consists of 3 major components:
1) **tenantmanager**: The _tenantmanager_ is responsible for creating and maintaining the API tokens for individual tenants. To do so it requires a _master_ API token that is able to create other tokens. In a Dynatrace Managed scenario it is also able to detect newly created tenants on a managed cluster and will automatically also add them to the API. So you can independently create tenants and they will be included to the consolidated API. This is very handy in environments where there are dynamically created environments (e.g. a service provider setup).
2) **tenantcache**: The _tenantcache_ is used to store meta-information about tenants and the current API tokens that are used to access the individual tenant APIs. The tenantcache is also used to keep the current tenats with some ways of filtering.
2) **apigateway**: The _apigateway_ is the core service which masks the original Dynatrace APIs and paralleizes the requests. Note that the specification of the original Dynatrae APIs is kept and the apigateway is independent of any Dynatrace API changes, but since it wraps one to may calls the response of the consolidated API is alsways an array of (Dynatrace API) responses. Keep this in mind when implementing against the consolidated API.


### Running the consolidated API
Originally I ran the consolidated api with a Docker-Compose stack, but today I rather run it in K8s - your choice. The Docker-compose stack also contains a _apiproxy_ service which is used for SSL termination and access restrictions. (I've not built in any other authorization or authentication mechanisms than basic auth - assuming that the consolidated API will run in a protected environment). The _apiproxy_ service can also take care of getting valid public certificates via Let's Encrypt (see more below).

#### With docker-compose
If you don't hava  K8s cluster available, the easiest way to run the consolidated API service is to build fresh with docker-compose:

In the docker directory create a ```.env``` file to add some configuration:

```
INT_API_GATEWAY=http://apigateway:8080      # leave http://apigateway:8080 when running in Docker (the devault container name)
INT_TENANTCACHE=tenantcache:6379            # leave tenantcache:6379 when running in Docker (the default container name of the redic cache)
DOMAIN=dtapi.dy.natrace.it                  # the domain name used for Let's encrypt certificates of the apiproxy nginx
API_HOST=https://dtapi.local:8443           # the (public) hostname of the consolidated API to access
LOG_LEVEL=info

REGISTRY=halfdome.local:50000/rweber
TAG=2.7
```

To protect unauthorized access to the consolidated API create a _.htpasswd_ file in the ```docker/apiproxy``` directory. You can add users/passwords to the _.htaccess_ file like this:
```
$ printf "${API_USER}:$(openssl passwd -apr1 ${API_PWD})\n" >> .htpasswd
```

The containers use some volume mounts for AWS credentials, Let's Enrcrypt data and certificates. If you do not want to make use of these you can just start with empty directories:

```
$ mkdir docker/aws
$ mkdir docker/certs
$ mkdir docker/accounts
```

##### Configuration of the tenantmanager
To tell the _tenantmanager_ which Dynatrace Managed or SaaS instances it should include in the consolidated API you will need to create a configuration with initial master tokens and endpoints. The configuration file is stored in ```docker/tenantmanager/config.json```. You can add multiple managed clusters or saas instances. Every _cluster_ entry should provide these fields:

| Attribute       | Values     |
| ------------- |:----------------------- |
| id            | A custom identifier for the tenant or managed cluster |
| type          | A free-form string to identify/select clusters. Can be used e.g. to separate develeopment from staging/production clusters in API queries |
| mode          | Either _saas_ or _managed_ depending on the endpoint. This is important for creating tokens as managed cluster admin tokens are handled differently than SaaS token management tokens |
| token         | The API token used to manage other tokens. For DT Managed mode this is a cluster admin token that allows the creation of tenant specific tokens globally. For SaaS this is a tenant specific token with permissions to create other tokens (token-management token)
| name          | A name for the tokens that will be created by the tenantmanager. | 


```
{
    "clusters" :
    [
      { 
        "id": "360Perf",
        "type": "partner",
        "mode": "saas",
        "url": "https://<tenantid>.live.dynatrace.com",
        "token": "<token management token>",
        "name" : "<name of the created tokens>"
      },
      {
        "id": "MyDTManaged",
        "type": "private",
        "mode": "managed",
        "url": "https://my.dynatrace-managed.com",
        "token": "<cluster management token>",
        "name" : "<name of the created tokens>"
      },
    ],
    "tokenduration": 43200,
    "scaninterval": 300,
    "apigateway": "http://apigateway:8080"
}
```

##### Build and run the consolidation API

Then build the containers by running ```docker-compose build``` in the docker directory.
Finally you can start the consolidation API by running ```docker-compose up -d```

If everything goes well you should see status output on the _tenantmanager_:

```
INFO:tenantmanager:===== Cluster/Tenant Update Starting =====
INFO:tenantmanager:  Scaninterval:	300
INFO:tenantmanager:  Tokentypes:	['DataExport', 'WriteConfig', 'ReadConfig', 'CaptureRequestData', 'DataPrivacy', 'MaintenanceWindows', 'ExternalSyntheticIntegration', 'PluginUpload', 'ReadAuditLogs', 'InstallerDownload', 'metrics.read', 'entities.read', 'entities.write', 'networkZones.read', 'networkZones.write', 'activeGates.read', 'activeGates.write']
INFO:tenantmanager:  Tokenduration:	43200
INFO:tenantmanager:   497/ 903 active/inactive tenants on server managed1 (https://managed1.my.dt.local)
INFO:tenantmanager:   398/ 703 active/inactive tenants on server managed2 (https://managed2.my.dt.local)
INFO:tenantmanager:   186/ 577 active/inactive tenants on server managed3 (https://managed3.my.dt.local)
INFO:tenantmanager:  1822/ 387 active/inactive tenants on server managed4 (https://managed4.my.dt.local)
INFO:tenantmanager:     1/   0 active/inactive tenants on server 360perf (https://mfk00070.live.dynatrace.com)
INFO:tenantmanager:  2904/2570 active/inactive tenants in total
INFO:tenantmanager:===== Cluster/Tenant Update Done (next in 300s) =====
INFO:tenantmanager:===== Tenant API-Token Check Starting =====
INFO:tenantmanager:   2904 tenants in cache
INFO:tenantmanager:No token found in cache for tenant tenant-token::360perf::partner::mfk00070::saas
INFO:tenantmanager:===== Tenant API Token Update Starting =====
INFO:tenantmanager:  token_types = ['DataExport', 'WriteConfig', 'ReadConfig', 'CaptureRequestData', 'DataPrivacy', 'MaintenanceWindows', 'ExternalSyntheticIntegration', 'PluginUpload', 'ReadAuditLogs', 'InstallerDownload', 'metrics.read', 'entities.read', 'entities.write', 'networkZones.read', 'networkZones.write', 'activeGates.read', 'activeGates.write']
INFO:tenantmanager:  parameters = {'clusterid': '360perf', 'tenantid': 'mfk00070'}
INFO:tenantmanager:  mode = saas
INFO:tenantmanager:  c_type = partner
INFO:tenantmanager:  Trying to create API tokens for 1 tenants via http://apigateway:8080/e/TENANTID/api/v1/tokens (there might be ignored inactive tenants)
INFO:tenantmanager:  Unknown CCVersion for tenant mfk00070 on cluster 360perf (mfk00070.live.dynatrace.com)
INFO:tenantmanager:  API Tokens created:    1 (Expiring at: 03.29.2021-23:26:55), this should match the number of active tenants
INFO:tenantmanager:===== Tenant API Token Update Finished =====
```

Once the tenantmanager has created tokens for all configured DT instances you should be able to query the consolidated API (with the basic auth from above). E.g. you can perform a single request to get all HOST entities accross all your Dynatrace instances and tenants (note that the API URL does use _TENANTID_ instaed of the real tenantids - I kept the URI format as in the original DT APIs - TENANTID will be dynamically be replaced by the _apigateway_)

```
$ curl -XGET https://dtapi.local/e/TENANTID/api/v2/entities?entitySelector=type("HOST")&from=now-5m&pageSize=500&fields=+properties.memoryTotal
```

The result will look something like this:
(Note the slighlty changed content compared to the original DT API response. Every response from an instance also contains the original DT host, id, and tenantid as well as the responcecode)

```
[
    {
        "clusterhost": "mfk00070.live.dynatrace.com",
        "clusterid": "360perf",
        "entities": [
            {
                "displayName": "spcmail",
                "entityId": "HOST-0CB15E0A1F8552DB",
                "properties": {
                    "memoryTotal": 2105864192
                }
            }
            ...
        ],
        "pageSize": 100,
        "responsecode": 200,
        "tenantid": "mfk00070",
        "totalCount": 7
    },
    {
        "clusterhost": "jtx55583.live.dynatrace.com",
        "clusterid": "mytestprod",
        "entities": [
            {
                "displayName": "gke-webshop-prod-e2-highcpu-16-preemptible-221",
                "entityId": "HOST-0192BAED720C28D1",
                "properties": {
                    "memoryTotal": 16792567808
                }
            }
            ...
        ],
        "pageSize": 100,
        "responsecode": 200,
        "tenantid": "jtx55583",
        "totalCount": 76
    },
    {
        "clusterhost": "rfv93504.live.dynatrace.com",
        "clusterid": "myteststage",
        "entities": [
            {
                "displayName": "gke-webshop-stage-n1-standard-8-non-preemptible-72",
                "entityId": "HOST-B30A02E1EF3923AC",
                "properties": {
                    "memoryTotal": 31566610432
                }
            }
            ...
        ],
        "pageSize": 100,
        "responsecode": 200,
        "tenantid": "rfv93504",
        "totalCount": 3
    }
]
```

