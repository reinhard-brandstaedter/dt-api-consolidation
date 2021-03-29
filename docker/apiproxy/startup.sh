#!/usr/bin/env bash

# Required to set environment. Dehydrated has issues otherwise.
env > /etc/environment

# Make sure we already registered with Let's Encrypt via dehydrated client
#/app/dehydrated/dehydrated --register --accept-terms

# ensure we have at least a self-signed certificate

DOMAIN=$(cat /etc/dehydrated/domains.txt)
CERTFILE=/etc/dehydrated/certs/${DOMAIN}/fullchain.pem
if [ -f "$CERTFILE" ]; then
    echo "$CERTFILE exists."
else
    echo "$CERTFILE doesn't exist, creating temporary self-signed certificate!"
    mkdir -p /etc/dehydrated/certs/${DOMAIN}
    openssl req -x509 -newkey rsa:4096 -nodes -subj /CN=${DOMAIN} -keyout /etc/dehydrated/certs/${DOMAIN}/privkey.pem -out /etc/dehydrated/certs/${DOMAIN}/fullchain.pem -days 30
fi

# Start cron
cron

envsubst < /etc/nginx/nginx.template > /etc/nginx/nginx.conf

nginx -g 'daemon off;' "$@"
