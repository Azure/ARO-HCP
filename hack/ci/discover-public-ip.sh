#!/bin/bash

for url in "https://ifconfig.me" "https://icanhazip.com" "https://ifconfig.co"; do
    if ! candidate=$(curl -sf -4 "$url"); then
        continue
    fi
    if [[ ! "$candidate" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
        continue
    fi
    IFS=. read -r -a octets <<< "$candidate"
    valid=true
    for octet in "${octets[@]}"; do
        if (( 10#$octet > 255 )) || [[ ${#octet} -gt 1 && "$octet" == 0* ]]; then
            valid=false
            break
        fi
    done
    if "$valid"; then
        printf '%s\n' "$candidate"
        exit 0
    fi
done

echo "ERROR: Could not determine public IP from any provider." >&2
exit 1
