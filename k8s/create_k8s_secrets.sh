#!/bin/sh

printf "${username}:`openssl passwd -apr1`\n" >> ./htpasswd

kubectl create secret generic -n dt-configmanagement htpasswd --from-file=./htpasswd
kubectl create secret generic -n dt-configmanagement cfg-tenantmanager --from-file=./config.json

