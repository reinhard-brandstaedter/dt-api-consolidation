package main

import (
	"net/http"
	urlpkg "net/url"
	"strings"
	"github.com/gomodule/redigo/redis"
	"sync"
	//"runtime"
	"time"
	"encoding/json"
	"log"
	"io"
	"io/ioutil"
    "os"
    "bytes"
	"strconv"
	//"math/rand"
	"crypto/tls"
)

const (
	// the LOCAL concurrency (maximum client side conenctions)
	CONCURRENCY = 700
	// server side concurrency (maximum connections to a server)
	SERVER_CONCURRENCY = 200
	CLIENT_TIMEOUT = 20
)

var (
	pool = newPool()
    Trace   *log.Logger
    Info    *log.Logger
    Warning *log.Logger
    Error   *log.Logger
    
    client *http.Client
)

func logInit(traceHandle io.Writer, infoHandle io.Writer, warningHandle io.Writer, errorHandle io.Writer) {

    Trace = log.New(traceHandle,
        "TRACE: ",
        log.Ldate|log.Ltime)
	    //|log.Lshortfile)

    Info = log.New(infoHandle,
        "INFO: ",
        log.Ldate|log.Ltime)
	    //|log.Lshortfile)

    Warning = log.New(warningHandle,
        "WARNING: ",
        log.Ldate|log.Ltime)
	    //|log.Lshortfile)

    Error = log.New(errorHandle,
        "ERROR: ",
        log.Ldate|log.Ltime)
	    //|log.Lshortfile)
}
    
func clientInit(){
	tr := &http.Transport{
		// Disable HTTP/2. - leads to issues with API
        TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		MaxIdleConns: 8000,
        MaxIdleConnsPerHost: 8000,
	}
	client = &http.Client {
		Transport: tr,
		Timeout: CLIENT_TIMEOUT * time.Second,
	}
}

func newPool() *redis.Pool {
	return &redis.Pool{
        MaxIdle: 2000,
		MaxActive: 0, // max number of connections
		//IdleTimeout: 30 * time.Second,
		Wait: true,
        Dial: func() (redis.Conn, error) {
            c, err := redis.Dial("tcp", "localhost:6379")
            if err != nil {
                    panic(err.Error())
            }
            return c, err
        },
	} 
}


func main() {
	//runtime.GOMAXPROCS(8)
	logInit(ioutil.Discard, os.Stdout, os.Stdout, os.Stderr)
	clientInit()
	http.HandleFunc("/e/", TenantProxyRoute)
	http.HandleFunc("/api/", ClusterProxyRoute)
	Info.Println("Starting up on 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getResponseChan(requests <-chan http.Request) <-chan []byte {
	//create a channel with buffer size equal to number of requests
	responses := make(chan []byte, cap(requests))
	var wg sync.WaitGroup
	
	// limiting the concurrency of goroutines for fetching responses
	sem := make(chan bool, CONCURRENCY)
	
	// execute requests and put results into response channel
	for req := range requests {
		wg.Add(1)
		sem <- true
		go func (wg *sync.WaitGroup, r http.Request) {
			defer func() { <-sem }()
			t_id := r.Header.Get("x-dt-tenant-id")
			c_id := r.Header.Get("x-dt-cluster-id")
			r.Header.Del("x-dt-tenant-id")
			r.Header.Del("x-dt-cluster-id")
			resp, err := client.Do(&r)
			//Error.Println(r.URL.RawQuery)
			if err != nil {
				Error.Println("Error on request", err)
			} else {
				content_type := resp.Header.Get("Content-Type")
				m := make(map[string]interface{})
				if t_id != "" {
					m["tenantid"] = t_id
				}
				m["clusterid"] = c_id
				m["clusterhost"] = r.Host
				m["responsecode"] = resp.StatusCode

				if 200 <= resp.StatusCode && resp.StatusCode <= 400 {
					body, err := ioutil.ReadAll(resp.Body)
					if err != nil {
						Error.Println("Can't read response:",err)
					}
					
					empty_body := false
					var f interface{}
					if len(body) == 0 {
						body = []byte("{\"responsecode\": "+strconv.Itoa(resp.StatusCode)+"}")
						empty_body = true
					}

					// ensure we do have a json response
					if (strings.Contains(content_type,"json") || empty_body) {
						err = json.Unmarshal(body, &f)
						if err != nil {
							Error.Println(err)
							//f = string(body)
						} else {
							switch f.(type) {
								case map[string]interface{}:
									m = f.(map[string]interface{})
								case int:
									m["value"] = f
								case float64:
									m["value"] = f
								default:
									m["result"] = f
							}
							if t_id != "" {
								m["tenantid"] = t_id
							}
							m["clusterid"] = c_id
							m["clusterhost"] = r.Host
							m["responsecode"] = resp.StatusCode
						}
					} else {
						Warning.Println("HTTP Response Content-Type not application/json:",string(body))
					}
				} else {
					Warning.Println("HTTP Response Status:",r.Method, r.URL, t_id, resp.StatusCode, http.StatusText(resp.StatusCode))
				}
				resp.Body.Close()
				json,_ := json.Marshal(m)
				responses <- json
			}
			wg.Done()
		}(&wg, req)
	}
	
	wg.Wait()
	close(responses)
	return responses
}

// creates a channel with request objects that need to be executed
// @keys: array of redis keys that point to the location/url of the tenant e.g.: tlocation::cmz-p1
func getTenantRequestChan(req *http.Request, keys []string, clusterlevel bool) <-chan http.Request {
	requests := make(chan http.Request, len(keys))
	var wg sync.WaitGroup
	uri := req.URL.RequestURI()
	var hdr_content_type string
	hdr_content_type = req.Header.Get("Content-Type")
	if hdr_content_type == "" {
		hdr_content_type = "application/json"
	}
	payload,_ := ioutil.ReadAll(req.Body)
	
	// cache-key to store tenant api token: 'tenant-token::<ccversion>::<cluster id>::<tenant uuid>'
	for _,tenantkey := range keys {
		wg.Add(1)
		//parallel requests to redis
		go func (wg *sync.WaitGroup, tenantkey string, clusterlevel bool) {
			rc := pool.Get()
			defer rc.Close()
			server,err := redis.String(rc.Do("GET",tenantkey))
			if err != nil {
				Error.Println(err)
			}
			tenant := strings.Split(tenantkey, "::")
			c_type := tenant[1]	//cluster type
			c_id := tenant[2]	//cluster ID
			t_id := tenant[3]	//tenant ID
			//Info.Println(tenant)
			// get mode (managed or saas) which is the last item in the tenantkey
			mode := tenant[len(tenant)-1]
			tokenkey := ""
			// cluster level request means we have to use the cluster API token for authentication
			if clusterlevel {
				tokenkey =	strings.Join([]string{"cluster-token",c_id},"::")
			} else {
				tokenkey = strings.Join([]string{"tenant-token",c_id,c_type,t_id,mode},"::")
			}
			token,_ := redis.String(rc.Do("GET",tokenkey))
			if mode == "saas" && token == "" {
				tokenkey =	strings.Join([]string{"cluster-token",c_id},"::")
				token,_ = redis.String(rc.Do("GET",tokenkey))
			} 
			if err != nil {
				Error.Println(err)
			}
			if token == "" {
				Error.Println("API Token ("+token+") is null for (mode: "+mode+") " + tokenkey)
			// no sense in creating request if we do not have an API token
			} else {
				url := server //+ strings.Replace(uri,"TENANTID",t_id,1)
				req,_ := http.NewRequest(req.Method, url, bytes.NewBuffer(payload))
				// make sure req Query is NOT urlencoded...seems DT has problems with some encoded params
				// using the Opaque attribute to enforce non-encoding, hope clients are sending clean queries
				
				// to also handle saas tenants (which do not need the "/e/TENANTID/" in the api) remove it in case of saas mode
				reqUri := uri
				if mode == "saas" {
					reqUri = strings.Replace(uri,"/e/TENANTID/","/",1)
				}

				req.URL = &urlpkg.URL{
					Scheme: req.URL.Scheme,
					Host: req.URL.Host,
					Opaque: "//"+req.URL.Host+strings.Replace(reqUri,"TENANTID",t_id,1),
				}
				
				req.Header.Add("Authorization", "Api-Token " + token)
				req.Header.Add("Content-Type", hdr_content_type)
				// special header to match with dt-proxy (haproxy) config to bypass ratelimiting
				req.Header.Add("x-dt-apigateway","rAteL1m1t0ff")
				// adding a tenant-id header to identify the response later
				req.Header.Add("x-dt-tenant-id", t_id)
				req.Header.Add("x-dt-cluster-id", c_id)
				
				requests <- *req
			}
			wg.Done()
		}(&wg,tenantkey,clusterlevel)
	}
	
	wg.Wait()
	close(requests)
	return requests
}

// creates a channel with request objects that need to be executed
// @keys: array of redis keys that point to the location/url of the cluster e.g.: clocation::fr1
func getClusterRequestChan(req *http.Request, keys []string, clusterlevel bool) <-chan http.Request {
	requests := make(chan http.Request, len(keys))
	var wg sync.WaitGroup
	uri := req.URL.RequestURI()
	var hdr_content_type string
	hdr_content_type = req.Header.Get("Content-Type")
	if hdr_content_type == "" {
		hdr_content_type = "application/json"
	}
	payload,_ := ioutil.ReadAll(req.Body)
	
	// cache-key to store cluster api token: 'cluster-token::<cc version>::<cluster id>'
	for _,clusterkey := range keys {
		wg.Add(1)
		//parallel requests to redis
		go func (wg *sync.WaitGroup, clusterkey string) {
			rc := pool.Get()
			defer rc.Close()
			server,err := redis.String(rc.Do("GET",clusterkey))
			if err != nil {
				Error.Println(err)
			}
			cluster := strings.Split(clusterkey, "::")
			//c_type := cluster[1]
			c_id := cluster[2] //cluster ID
			tokenkey := strings.Join([]string{"cluster-token",c_id},"::")
			token,_ := redis.String(rc.Do("GET",tokenkey))
			if err != nil {
				Error.Println(err)
			}
			url := server + uri
			req,_ := http.NewRequest(req.Method, url, bytes.NewBuffer(payload))
			req.Header.Add("Authorization", "Api-Token " + token)
			req.Header.Add("Content-Type", hdr_content_type)
			// special header to match with dt-proxy (haproxy) config to bypass ratelimiting
			req.Header.Add("x-dt-apigateway","rAteL1m1t0ff")
			// adding a cluster-id header to identify the response later
			req.Header.Add("x-dt-cluster-id", c_id)
			requests <- *req
			wg.Done()
		}(&wg,clusterkey)
	}
	
	wg.Wait()
	close(requests)
	return requests
}


// combines the []byte responses stored in a response channel from multiple requests into on
// map that can be converted into a json response
func combineResponses(responses <-chan []byte) []map[string]interface{} {
	combined := make([]map[string]interface{},0)
	for response := range responses {
		i := 0
		var f interface{}
		err := json.Unmarshal(response, &f)
		if err != nil {
			Error.Println(err)
		}
		m := f.(map[string]interface{})
		combined = append(combined,m)
		i++
	}
	return combined
}

// ensure the request keys are distributed/ordered so that there aren't too many requests per cluster
// but also ensure that as may requests as possible are done in parallel
// this is done by calculating a dynamic concurrency (up to the max a the http client can handle)
// return a accordingly sorted slice of keys
func distributeRequests(keys []string) []string{
	var distributed []string
	var clusters = make(map[string][]string)

	//create cluster map of tenant keys
	for _,key := range keys {
		tokens := strings.Split(key, "::")
		c_id := tokens[2]
		clusters[c_id] = append(clusters[c_id],key)
	}

	// create slice that is filled with 'buckets' of maximum concurrency per cluster tenant keys
	c := len(clusters)
	for c > 0 {
		for k,_ := range clusters {
			//Info.Println(k +": "+strconv.Itoa(len(clusters[k])))
			size := SERVER_CONCURRENCY
			if len(clusters[k]) < size {
				size = len(clusters[k])
			}
			distributed = append(distributed, clusters[k][:size]...)
			clusters[k] = clusters[k][size:]
			if len(clusters[k]) == 0 { // all keys have been assigned, remove from map
				delete(clusters,k)
				c -= 1
			}
		}
	}

	return distributed
}

func makeMultiRequests(req *http.Request, key string, getReqChan func(*http.Request, []string, bool) <-chan http.Request, getRespChan func(<-chan http.Request) <-chan []byte, clusterlevel bool) ([]map[string]interface{},int,int,int) {
	rediscon := pool.Get()
	defer rediscon.Close()
	keys,err := redis.Strings(rediscon.Do("KEYS", key))
	if err != nil {
		Error.Println(err)
	}

	// shuffle the keys as they define which reuqests are made in parallel
	// this helps to distribute the load across clusters
	//rand.Seed(time.Now().UnixNano())
	//rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })

	keys = distributeRequests(keys)
	// debug spread of cluster requests in batches of CONCURRENCY
	/*
	var clusters = make(map[string]int)
	var str []string
	for i, key := range keys {
		tokens := strings.Split(key, "::")
		c_id := tokens[2]
		clusters[c_id] += 1
		if i > 0 && i % CONCURRENCY == 0 {
			str = nil
			for k,v := range clusters {
				str = append(str,k +": "+strconv.Itoa(v))
			}
			Info.Println("Bucket: "+strconv.Itoa(i/CONCURRENCY) + ": " + strings.Join(str,", "))
			clusters = make(map[string]int)
		}
	}
	// print remainder bucket
	str = nil
	for k,v := range clusters {
		str = append(str,k +": "+strconv.Itoa(v))
	}
	Info.Println("Last Bucket: " + strings.Join(str,", "))
	*/
	
	// create all http requests that need to be executed in a channel
	//Info.Println("Endpoints to query: ",len(keys))
	nr_endpoints := len(keys)
	chanRequests := getReqChan(req, keys, clusterlevel)
	//Info.Println("Requests created: ",len(chanRequests))
	nr_requests := len(chanRequests)
	
	// create channel with all responses
	chanResponses := getRespChan(chanRequests)
	//Info.Println("Responses received: ",len(chanResponses))
	nr_responses := len(chanResponses)
	
	//combine the responses
	combined := combineResponses(chanResponses)
	return combined, nr_endpoints, nr_requests, nr_responses
}

func getFilterFromQuery(filter []string, req *http.Request) string {
	var filtervalues []string
	filtervalues = append(filtervalues,filter[0])
	
	query_params := req.URL.Query()
	for i:=1; i<len(filter); i++ {
		value := query_params[filter[i]]
		if value !=  nil {
			filtervalues = append(filtervalues,value[0])
		} else {
			filtervalues = append(filtervalues,"*")
		}
	}
	
	//tenant-url::<type>::<cluster id>::<tenant uuid>::<evironment stage>::<customer id>::<saas|managed>
	keyfilter := strings.Join(filtervalues,"::")
	
	return keyfilter
}

func removeQueryFromReq(filter []string, req *http.Request) {
	query := req.URL.Query()
	
	for i:=1; i<len(filter); i++ {
		query.Del(filter[i])
	}
	// net/http per default encodes all query parameters, to allow query parameters with special chars we need to explicitly unescape
	//req.URL.RawQuery,_ = urlpkg.PathUnescape(query.Encode())
}

func GetStatusCode(response []map[string]interface{}) int {
	// generate appropriate HTTP response codes: check the individual response codes of the responses
	// if all 20x => 20x, if all 40x => 40x else 207 (multi status)
	var statcnt map[int]int
	statcnt = make(map[int]int)
	
	code := 0
	for i := range response {
		code = int(response[i]["responsecode"].(float64))
		if cnt, ok := statcnt[code]; ok {
		    statcnt[code] = cnt + 1
		} else {
			statcnt[code] = 1
		}
	}
	
	returnstatus := 0
	
	// if there is only one status in the response use it as overall response, if more return multistatus
	if len(statcnt) == 1 {
		for status,_ := range statcnt {
			returnstatus = status
		}
	} else {
		returnstatus = http.StatusMultiStatus
	}
	
	//Info.Println("HTTP status:",returnstatus)
	return returnstatus
}

func TenantProxyRoute(w http.ResponseWriter, req *http.Request) {
	uri := req.URL.RequestURI()
	method := req.Method
	nr_endpoints := 0
	nr_requests := 0
	nr_responses := 0
	statuscode := 0
	var response []map[string]interface{}
	
	filterset := []string {
		"tenant-url",
		"type",
		"clusterid",
		"tenantid",
		"stage",
		"customerid",
		"mode"}
		 
	keyfilter := getFilterFromQuery(filterset,req)
	removeQueryFromReq(filterset,req)
	
	//Info.Println(method + " " + uri)
	//Info.Println("Query Filter: " + keyfilter)
	t1 := time.Now()
	
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if strings.Contains(uri,"TENANTID") {
		clusterlevel := !strings.HasPrefix(uri, "/e/")
		response,nr_endpoints,nr_requests,nr_responses = makeMultiRequests(req,keyfilter,getTenantRequestChan,getResponseChan,clusterlevel)
		statuscode = GetStatusCode(response)
		w.WriteHeader(statuscode)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
	t2 := time.Now()
	diff := t2.Sub(t1)

	logs := []string {method + " " + uri, keyfilter, "HTTP"+strconv.Itoa(statuscode), "Endpoints:"+strconv.Itoa(nr_endpoints), "REQ:"+strconv.Itoa(nr_requests), "RESP:"+strconv.Itoa(nr_responses), "TIME:"+diff.String()}
	Info.Println(strings.Join(logs,", "))
	json.NewEncoder(w).Encode(response)
}

func ClusterProxyRoute(w http.ResponseWriter, req *http.Request) {
	uri := req.URL.RequestURI()
	method := req.Method
	nr_endpoints := 0
	nr_requests := 0
	nr_responses := 0
	statuscode := 0
	var response []map[string]interface{}
	
	// either do a tenant-specific request to the cluster (e.g. Tenant configs) ...
	if strings.Contains(uri,"TENANTID") {
		TenantProxyRoute(w,req)
	} else {
	// ... or a cluster specific request
		filterset := []string {
			"cluster-url",
			"type",
			"clusterid",
			"mode"}

		// cluster requests are only allowed for managed cluster types, enforce it, regardless of the original query
		q := req.URL.Query()
    	q.Add("mode", "managed")
    	req.URL.RawQuery = q.Encode()
		
		keyfilter := getFilterFromQuery(filterset,req)
		removeQueryFromReq(filterset,req)
		
		//Info.Println(method + " " + uri)
		//Info.Println("Query Filter: " + keyfilter)
		t1 := time.Now()
		
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		response,nr_endpoints,nr_requests,nr_responses = makeMultiRequests(req,keyfilter,getClusterRequestChan,getResponseChan,true)
		statuscode = GetStatusCode(response)
		w.WriteHeader(statuscode)
		
		t2 := time.Now()
		diff := t2.Sub(t1)
	
		logs := []string {method + " " + uri, keyfilter, "HTTP"+strconv.Itoa(statuscode), "Endpoints:"+strconv.Itoa(nr_endpoints), "REQ:"+strconv.Itoa(nr_requests), "RESP:"+strconv.Itoa(nr_responses), "TIME:"+diff.String()}
		Info.Println(strings.Join(logs,", "))
		json.NewEncoder(w).Encode(response)
	}
}