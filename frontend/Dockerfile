# Base and builder image will need to be replaced by Fips compliant one
FROM registry.access.redhat.com/ubi8/ubi-minimal:8.9-1161 AS builder
RUN microdnf install -y tar make git
RUN curl -sL https://go.dev/dl/go1.22.2.linux-amd64.tar.gz -o go.tar.gz \
    && tar -C /usr/local -xzf go.tar.gz \
    && rm -f go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"
ENV PATH="${GOPATH}/bin:${PATH}"

WORKDIR /app
COPY . .
RUN cd frontend && make frontend

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.9-1161

ENV USER_UID=1001 \
    USER_NAME=frontend

COPY --from=builder /app/frontend/aro-hcp-frontend /usr/local/bin/
ENTRYPOINT ["aro-hcp-frontend"]
USER ${USER_UID}
EXPOSE 8443/tcp
