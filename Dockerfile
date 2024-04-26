# Stage 1: Build the Golang binary
FROM golang:1.22 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go module files
COPY go.mod go.sum ./

# Download the Go dependencies
RUN go mod download

# Copy the source code to the container
COPY . .

# Build the Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o secret-controller .

# Stage 2: Create the final lightweight image
FROM scratch
# Copy the binary from the first stage
COPY --from=builder /app/secret-controller /secret-controller

# Expose any necessary ports
EXPOSE 8080

# Set the command to run the binary
CMD ["/secret-controller"]
