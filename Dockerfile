FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=1.0.3
ARG COMMIT=dev
ARG BUILT_AT=unknown
RUN go test ./... && CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILT_AT}" \
    -o /out/sfs ./cmd/sfs

FROM alpine:3.21
RUN adduser -D -u 10001 sfs
USER sfs
WORKDIR /home/sfs
COPY --from=build /out/sfs /usr/local/bin/sfs
ENTRYPOINT ["sfs"]
CMD ["version"]
