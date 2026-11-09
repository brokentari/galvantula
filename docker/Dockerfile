# A Linux host for `factory serve`: bwrap, Go and claude, all where the sandbox finds them.
# The sandbox sees only /usr and /etc, and finds commands only in /usr/bin.
FROM golang:1.27-bookworm AS go
FROM node:22-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends bubblewrap git redis-server redis-tools && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
RUN ln -s /usr/local/go/bin/go /usr/local/go/bin/gofmt /usr/local/bin/node /usr/bin/ \
 && npm install -g @anthropic-ai/claude-code@2.1.319 \
 && ln -s /usr/local/bin/claude /usr/bin/claude
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
