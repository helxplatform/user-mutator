# Use the official Golang image to build the binary
FROM golang:1.23 AS builder

ENV CGO_ENABLED=0

# Copy the Go source files, Makefile, etc.
COPY webhook-server /build

# Set the working directory
WORKDIR /build

RUN go build -o user-mutator

FROM alpine:3.18

# Ensure we have a valid user and group
RUN addgroup -g 1000 -S helx && \
    adduser -u 1000 -h /app -G helx -S helx

# Copy main application
COPY --from=builder --chown=helx:helx /build/user-mutator /app

USER helx
WORKDIR /app
# Expose port 8443
EXPOSE 8443

# Run the compiled binary
CMD ["/app/user-mutator"]
