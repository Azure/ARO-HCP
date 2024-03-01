    
    TARGETFILE=$1
    echo "########## VPN Configuration for RG $RESOURCEGROUP to localsecrets/$TARGETFILE ##########"
    mkdir -p localsecrets
    curl -so vpnclientconfiguration.zip "$(az network vnet-gateway vpn-client generate \
        -g "$RESOURCEGROUP" \
        -n dev-vpn \
        -o tsv)"
    export CLIENTCERTIFICATE="$(openssl x509 -inform der -in secrets/vpn-client.crt)"
    export PRIVATEKEY="$(openssl rsa -inform der -in secrets/vpn-client.key)"
    unzip -qc vpnclientconfiguration.zip 'OpenVPN\\vpnconfig.ovpn' \
        | envsubst \
        | grep -v '^log ' >"localsecrets/$TARGETFILE"
    rm vpnclientconfiguration.zip
