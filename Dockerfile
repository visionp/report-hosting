# Docker is used to build and verify the Linux binary; production runs systemd.
FROM golang:1.26-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS source
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM source AS test
RUN apt-get update && apt-get install -y --no-install-recommends nginx curl openssl ca-certificates python3 shellcheck && rm -rf /var/lib/apt/lists/*
RUN shellcheck deploy/scripts/deploy-release.sh && python3 -m unittest discover -s deploy/tests -v
RUN go vet ./... && go test -race -count=1 ./...
RUN bash deploy/scripts/nginx-itest.sh

FROM source AS build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/report_hosting ./cmd/report_hosting

FROM scratch AS binary
COPY --from=build /out/report_hosting /report_hosting
