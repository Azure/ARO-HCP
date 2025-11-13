
curl https://raw.githubusercontent.com/Azure/ARO-Tools/refs/heads/main/pkg/config/ev2config/config.yaml -o - \
    | yq -o=json '.clouds.public.regions | .[] |= {"geoShortId": .geoShortId, "geography": .geography}'