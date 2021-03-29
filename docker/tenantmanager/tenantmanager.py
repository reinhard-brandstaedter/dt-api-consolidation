import time, sys, os
import requests, json, urllib3
import redis
import logging
import random
import math
from datetime import datetime
from threading import Timer
from urllib.parse import urlparse
import tenanthelpers.dtTenant as Tenant

# LOG CONFIGURATION
FORMAT = '%(asctime)s:%(levelname)s:%(name)s:%(message)s'
logging.basicConfig(stream=sys.stdout, level=logging.INFO, format=FORMAT)
logger = logging.getLogger("tenantmanager")
logging.getLogger("urllib3").setLevel(logging.WARNING)
logging.getLogger("requests").setLevel(logging.WARNING)

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

redishost = os.getenv('INT_TENANTCACHE', 'localhost:6379')
tenantcache = redis.StrictRedis(host=redishost.split(":")[0], port=redishost.split(":")[1], db=0, charset="utf-8", decode_responses=True)

configfile = '/config/config.json'
tokenduration = 14400
scaninterval = 120
apigateway = "http://" + os.getenv('INT_API_GATEWAY', "localhost:8080")
tenant_token_types = ["DataExport","WriteConfig","ReadConfig","CaptureRequestData","DataPrivacy","MaintenanceWindows","ExternalSyntheticIntegration","PluginUpload","ReadAuditLogs","InstallerDownload","metrics.read","entities.read","entities.write","networkZones.read","networkZones.write","activeGates.read","activeGates.write"]

def createSaasTenantToken():
    pass

def createManagedTenantToken():
    pass

def createTenantAPITokens(**kwargs):
    logger.info('===== Tenant API Token Update Starting =====')
    if kwargs is not None:
        for key, value in kwargs.items():
            logger.info('  {} = {}'.format(key,value))

    scopes = json.dumps(kwargs["token_types"])
    parameters = kwargs["parameters"]

    mode = kwargs["mode"]
    c_type = ""
    if "c_type" in kwargs:
        c_type = kwargs["c_type"]

    if mode == "saas":
        url = apigateway+'/e/TENANTID/api/v1/tokens'
        tokendto = '{\
            "name": "configManagementMaster", \
            "expiresIn": { \
                "value": '+str(tokenduration)+', \
                "unit": "SECONDS" \
            },\
            "scopes": '+scopes+'\
        }'
        tokenkey = "token"
    else:
        url = apigateway+'/api/v1.0/control/tenantManagement/tokens/TENANTID'
        tokendto = '{ \
            "scopes": '+ scopes +', \
            "label": "configManagementMaster", \
            "expirationDurationInSeconds": "' + str(tokenduration) + '" \
        }'
        tokenkey = "tokenId"
        
    now = datetime.utcnow()
    expirytime = datetime.fromtimestamp(now.timestamp()+tokenduration)

    tenanttokens = {}
    created = 0
    
    try:
        response = requests.post(url, data=tokendto, params=parameters, timeout=60, verify=False)
        tenanttokens = response.json()
        logger.info("  Trying to create API tokens for {} tenants via {} (there might be ignored inactive tenants)".format(len(tenanttokens), url))
        
        for tenant in tenanttokens:
            try:
                t_id = tenant["tenantid"]
                c_id = tenant["clusterid"]
                c_url = tenant["clusterhost"]
                token = tenant[tokenkey]
                if c_type == "":
                    c_type = Tenant.getCCVersion(t_id)
                # only if it's an active tenant also create an cache entry for the token
                t_type = c_type if Tenant.getCCVersion(t_id) == "unknown" else Tenant.getCCVersion(t_id)
                checkkey = "::".join(["tenant-url", t_type, c_id.lower(), t_id, Tenant.getEnvStage(t_id), Tenant.getCustomer(t_id)])
                found = tenantcache.keys(checkkey)
                if found != None:
                    cache_key = "::".join(["tenant-token", c_id.lower(), t_type, t_id, mode])
                    
                    if "unknown" == Tenant.getCCVersion(t_id):
                        logger.info("  Unknown CCVersion for tenant {} on cluster {} ({})".format(t_id,c_id,c_url))
                    
                    #logger.info('  CREATE TOKEN for [{:16s}] on [{}], caching (Expiry: {})'.format(cache_key,c_id,tokenduration))
                    tenantcache.setex(cache_key,tokenduration,token)
                    created += 1
            except:
                logger.error("Problem creating apitoken for {}::{} {}".format(c_id, t_id, sys.exc_info()))
                logger.error("Tenant response: {}".format(tenant))
                continue
    except:
        logger.error("Problem creating apitoken {}".format(sys.exc_info()))
    
    logger.info('  API Tokens created: {:>4} (Expiring at: {}), this should match the number of active tenants'.format(created,expirytime.strftime('%m.%d.%Y-%H:%M:%S')))    
    logger.info('===== Tenant API Token Update Finished =====')    
    
# get all active tenants
def getDTManagedTenants(serverid, serverurl, c_token, c_type):
    mode = "managed"
    tenantsession = requests.Session()
    tenantsession.trust_env = False
    tenanttokens = {}
    active = inactive = 0

    url = serverurl+'/api/v1.0/control/tenantManagement/tenantConfigs'
    headers = {'Authorization':'Api-Token '+c_token,  'Accept': 'application/json', 'x-dt-apigateway':'rAteL1m1t0ff'}
    
    try:
        response = tenantsession.get(url, headers=headers, timeout=60, verify=False)
        tenants = response.json()
        for tenant in tenants:
            if tenant["isActive"]:
                t_id = tenant["tenantUUID"]
                t_type = c_type if Tenant.getCCVersion(t_id) == "unknown" else Tenant.getCCVersion(t_id)
                key = "::".join(["tenant-url", t_type, serverid.lower(), t_id, Tenant.getEnvStage(t_id), Tenant.getCustomer(t_id),mode])
                tenantcache.setex(key,tokenduration,serverurl)
                # create a set with all tenant IDs (clusterid::tenantid)
                tenantcache.sadd("tenants","::".join([serverid.lower(),t_type,t_id,mode]))
                active += 1
            else:
                inactive += 1
    except:
        logger.error("Problem getting tenants from server {} ({}) : {}".format(serverurl, serverid, sys.exc_info()))
            
    logger.info("  {:>4}/{:>4} active/inactive tenants on server {} ({})".format(active, inactive, serverid, serverurl))
    tenantsession.close()
    return active, inactive

def getDTSaasTenants(serverid, serverurl, c_token, c_type):
    mode = "saas"
    tenantsession = requests.Session()
    tenantsession.trust_env = False
    tenanttokens = {}
    active = inactive = 0

    #extract tenantid from serverurl
    # create tenantcache key based on serverid, tenantid, token
    t_id = urlparse(serverurl).hostname.split(".")[0]

    try:
        t_type = c_type if Tenant.getCCVersion(t_id) == "unknown" else Tenant.getCCVersion(t_id)
        key = "::".join(["tenant-url", t_type , serverid.lower(), t_id, Tenant.getEnvStage(t_id), Tenant.getCustomer(t_id), mode])
        tenantcache.setex(key,tokenduration,serverurl)
        # create a set with all tenant IDs (clusterid::tenantid)
        tenantcache.sadd("tenants","::".join([serverid.lower(),t_type,t_id,mode]))
        active += 1
    except:
        logger.error("Problem getting tenants from server {} ({}) : {}".format(serverurl, serverid, sys.exc_info()))
            
    logger.info("  {:>4}/{:>4} active/inactive tenants on server {} ({})".format(active, inactive, serverid, serverurl))
    tenantsession.close()
    return active, inactive

def checkTokenExpiry():
    logger.info('===== Tenant API-Token Check Starting =====')
    hastoken = notoken = 0
    tenants = tenantcache.smembers("tenants")
    logger.info("   {} tenants in cache".format(len(tenants)))
    t_notoken = set()
    t_lowtoken = set()
    low_ttl = 600
    #logger.info("Tenants: {}".format(tenants))
    for tenant in tenants:
        key = "::".join(["tenant-token",tenant])
        t_items = tenant.split("::")
        c_id = t_items[0]
        t_type = t_items[1]
        t_id = t_items[2]
        mode = t_items[3] if len(t_items) == 4 else "managed"
        token = tenantcache.get(key)
        if token != None:
            hastoken += 1
            #check token expiry ttl
            ttl = tenantcache.ttl(key)
            
            # the majority of tokens should expire at the same time, so we try to push a global token refresh when the first expiry is detected and exit
            # if not every tenant gets a token refresh the next interval will trigger again
            if ttl < low_ttl:
                logger.info("  Token for {} will expire in {:>3.0f} minutes".format(key,ttl/60))
                if mode == "saas":
                    #immediately create token
                    createTenantAPITokens(token_types=tenant_token_types,parameters={"clusterid":c_id, "tenantid":t_id},mode="saas",c_type=t_type)
                else:
                    t_lowtoken.add(tenant)
        else:
            logger.info("No token found in cache for tenant {}".format(key))
            notoken += 1
            # must create token, adding to the list of tenants to decide later if a batch or individual creation should be performed
            if mode == "saas":
                #immediately create token
                createTenantAPITokens(token_types=tenant_token_types,parameters={"clusterid":c_id, "tenantid":t_id},mode="saas",c_type=t_type)
            else:
                t_notoken.add(tenant)
     
    logger.info("  {} tenants in cache, {} with token ({} expiring soon), {} with no token (will be created)".format(len(tenants), hastoken, len(t_lowtoken), notoken))
    
    # if many tokens expire soon or a lot of tenants have no token, force a refresh
    if len(t_lowtoken) > 2000 or len(t_notoken) > 2000:
        logger.info("  Triggering batch token updates as too many are expiring ({}) or no token was found ({})".format(len(t_lowtoken),len(t_notoken)))
        # first handle the no-tokens, split load a bit by stages
        #for tenant in t_notoken:
        createTenantAPITokens(token_types=tenant_token_types,parameters={},mode="managed")
    
    # if the number of tenants with no token is low, trigger individual token creation per tenant
    if len(t_notoken) <= 2000:
        for tenant in t_notoken:
            t_items = tenant.split("::")
            c_id = t_items[0]
            t_type = t_items[1]
            t_id = t_items[2]
            mode = t_items[3] if len(t_items) == 4 else "managed"
            logger.info("  Creating new token for {} as none was found".format(tenant))
            createTenantAPITokens(token_types=tenant_token_types,parameters={"clusterid":c_id, "tenantid":t_id},mode="managed",c_type=t_type)
            
    # if the number of tenants with expiring token is low, trigger individual token creation per tenant       
    if len(t_lowtoken) <= 2000:
        for tenant in t_lowtoken:
            t_items = tenant.split("::")
            c_id = t_items[0]
            t_type = t_items[1]
            t_id = t_items[2]
            mode = t_items[3] if len(t_items) == 4 else "managed"
            logger.info("  Creating new token for {} as it's expiring soon".format(tenant))
            createTenantAPITokens(token_types=tenant_token_types,parameters={"clusterid":c_id, "tenantid":t_id},mode="managed",c_type=t_type)
         
    logger.info('===== Tenant API-Token Check Done =====')

def readConfig(configfile):
    global scaninterval
    global tokenduration
    
    #TODO: errorhandling
    with open(configfile) as json_file:  
        config = json.load(json_file)
        scaninterval = config['scaninterval']
        tokenduration = config['tokenduration']
        
    return config['clusters']
    
def main(argv):
    global scaninterval
    global tokenduration
    global tenant_token_types
    readConfig(configfile)
    
    # Main Loop to update clusters configs and Tenants
    while True:
        logger.info('===== Cluster/Tenant Update Starting =====')
        clusters = readConfig(configfile)
        # allows to change configs on the fly 
        logger.info("  Scaninterval:\t{}".format(scaninterval))
        logger.info("  Tokentypes:\t{}".format(tenant_token_types))
        logger.info("  Tokenduration:\t{}".format(tokenduration))
        totalactive = totalinactive = 0
        
        # delete tenants set from tenantcache to ensure that inactive or deleted tenants are not considerd for token updates
        tenantcache.delete("tenants")
        
        for cluster in clusters:
            c_id = cluster['id'].lower()
            c_token = cluster['token']
            c_url = cluster['url'].lower()
            c_type = cluster['type'].lower()
            c_mode= cluster['mode'].lower()
            # cache-key: 'cluster::fr1'
            cache_key = "::".join(["cluster-token", c_id]).lower()
            tenantcache.setex(cache_key,tokenduration,c_token)
            cache_key = "::".join(["cluster-url",c_type,c_id,c_mode]).lower()
            tenantcache.setex(cache_key,tokenduration,c_url)
            #logger.info("Fetching Tenants on Server: {} {}".format(c_id,c_url))
            
            managed = (cluster['mode'] == "managed")
            if managed:
                active, inactive = getDTManagedTenants(c_id,c_url,c_token,c_type)
            else:
                active, inactive = getDTSaasTenants(c_id,c_url,c_token,c_type)
            
            totalactive += active
            totalinactive += inactive
            
        logger.info("  {:>4}/{:>4} active/inactive tenants in total".format(totalactive, totalinactive))
        logger.info('===== Cluster/Tenant Update Done (next in {}s) ====='.format(scaninterval))
        
        checkTokenExpiry()
        time.sleep(scaninterval)
    
if __name__ == "__main__":
   main(sys.argv[1:])

