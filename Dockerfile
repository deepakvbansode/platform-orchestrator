# --- Stage 1: build the orchestrator binary ---
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Cache dependency downloads separately from source compilation.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /score-orchestrator .

# --- Stage 2: fetch score-k8s ---
FROM alpine:3.21 AS score-k8s-fetcher

ARG SCORE_K8S_VERSION=0.10.1
ARG TARGETARCH

RUN apk add --no-cache curl tar && \
    curl -fsSL \
      "https://github.com/score-spec/score-k8s/releases/download/${SCORE_K8S_VERSION}/score-k8s_${SCORE_K8S_VERSION}_linux_${TARGETARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin score-k8s && \
    chmod +x /usr/local/bin/score-k8s

# --- Stage 3: fetch kubectl ---
FROM alpine:3.21 AS kubectl-fetcher

ARG KUBECTL_VERSION=v1.32.3
ARG TARGETARCH

RUN apk add --no-cache curl && \
    curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
      -o /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl

# --- Stage 4: runtime image ---
FROM alpine:3.21

RUN apk add --no-cache git

COPY --from=builder           /score-orchestrator           /usr/local/bin/score-orchestrator
COPY --from=score-k8s-fetcher /usr/local/bin/score-k8s      /usr/local/bin/score-k8s
COPY --from=kubectl-fetcher   /usr/local/bin/kubectl        /usr/local/bin/kubectl
# Default config — override at runtime by mounting a ConfigMap at /etc/orchestrator.
COPY orchestrator.yaml        /etc/orchestrator/orchestrator.yaml

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/score-orchestrator", "server"]
