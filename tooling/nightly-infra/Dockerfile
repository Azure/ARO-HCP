# azurelinux3.0 from Apr 1st 2025
FROM mcr.microsoft.com/azure-cli@sha256:18ec5cf02ba6b46e9858ddff1125baaa1f14ba919f77ebc67c918c891f8df4a2

# Install Make, Git and Bash via tdnf
RUN tdnf install -y make git bash \
    && tdnf clean all

WORKDIR /app

# Copy your Makefile and scripts
COPY Makefile   /app/Makefile
COPY scripts/   /app/scripts/

# Make your entrypoint script executable
RUN chmod +x /app/scripts/deploy.sh

# Default entrypoint: run deploy.sh
ENTRYPOINT [ "/app/scripts/deploy.sh" ]
