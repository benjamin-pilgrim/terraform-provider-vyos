#!/bin/vbash
# All inputs come from the disposable seed ISO. No existing router config is read.
source /opt/vyatta/etc/functions/script-template
configure
set system host-name provider-ci
set system config-management commit-confirm action reload
set interfaces ethernet eth0 address 10.0.2.15/24
set pki certificate PROVIDER-CI certificate "$(sed '/---/d' /mnt/ci/api.pem | tr -d '\n')"
set pki certificate PROVIDER-CI private key "$(sed '/---/d' /mnt/ci/api.key | tr -d '\n')"
set service https certificates certificate PROVIDER-CI
set service https listen-address 10.0.2.15
set service https api rest
set service https api keys id provider-ci key '@API_KEY@'
set service ssh
set firewall ipv4 name TF-CI-SENTINEL default-action drop
commit || builtin exit 12
save || builtin exit 13
exit
