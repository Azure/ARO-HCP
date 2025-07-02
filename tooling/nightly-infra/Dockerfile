# azurelinux3.0 from Apr 1st 2025
FROM mcr.microsoft.com/azure-cli@sha256:18ec5cf02ba6b46e9858ddff1125baaa1f14ba919f77ebc67c918c891f8df4a2

# Install dependencies
RUN tdnf install -y make git bash \
    && tdnf clean all \
    && rm -rf /var/cache/tdnf

# Set working directory
WORKDIR /app

# Copy files
COPY scripts/ /app/scripts/
COPY ../../*.mk /app/
COPY ../../Makefile /app/Makefile

# Set permissions efficiently
RUN chmod +x /app/scripts/deploy.sh

# Default entrypoint: run deploy.sh
ENTRYPOINT ["/app/scripts/deploy.sh"]
