package production

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeploymentInfo holds deployment metadata.
type DeploymentInfo struct {
	Environment    string
	Version        string
	Revision       string
	DeploymentTime time.Time
	DeployedBy     string
	BuildNumber    string
	DockerImage    string
}

// NewDeploymentInfo creates deployment info from environment variables.
func NewDeploymentInfo() *DeploymentInfo {
	return &DeploymentInfo{
		Environment:    getEnvString("ENVIRONMENT", "development"),
		Version:        getEnvString("VERSION", "unknown"),
		Revision:       getEnvString("REVISION", "unknown"),
		DeploymentTime: time.Now(),
		DeployedBy:     getEnvString("DEPLOYED_BY", "unknown"),
		BuildNumber:    getEnvString("BUILD_NUMBER", "unknown"),
		DockerImage:    getEnvString("DOCKER_IMAGE", "unknown"),
	}
}

// IsProduction checks if this is a production deployment.
func (di *DeploymentInfo) IsProduction() bool {
	return di.Environment == "production"
}

// DeploymentValidator validates deployment readiness.
type DeploymentValidator struct {
	requiredEnvVars []string
	requiredPaths   []string
}

// NewDeploymentValidator creates a new deployment validator.
func NewDeploymentValidator() *DeploymentValidator {
	return &DeploymentValidator{
		requiredEnvVars: []string{
			"ENVIRONMENT",
			"VERSION",
			"DOCKER_IMAGE",
		},
		requiredPaths: []string{
			"/etc/cadre/config",
			"/var/log/cadre",
		},
	}
}

// ValidateEnvironment checks if all required environment variables are set.
func (dv *DeploymentValidator) ValidateEnvironment() error {
	var missing []string

	for _, envVar := range dv.requiredEnvVars {
		if os.Getenv(envVar) == "" {
			missing = append(missing, envVar)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return nil
}

// ValidatePaths checks if required paths exist or can be created.
func (dv *DeploymentValidator) ValidatePaths() error {
	var invalid []string

	for _, path := range dv.requiredPaths {
		if err := ensurePathExists(path); err != nil {
			invalid = append(invalid, path)
		}
	}

	if len(invalid) > 0 {
		return fmt.Errorf("invalid paths: %s", strings.Join(invalid, ", "))
	}

	return nil
}

// PreFlightCheck performs all pre-deployment checks.
func (dv *DeploymentValidator) PreFlightCheck() error {
	if err := dv.ValidateEnvironment(); err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	if err := dv.ValidatePaths(); err != nil {
		return fmt.Errorf("path validation failed: %w", err)
	}

	return nil
}

// ServiceLoadBalancer manages service instances for load balancing.
type ServiceLoadBalancer struct {
	instances []ServiceInstance
	current   int
}

// ServiceInstance represents a service instance.
type ServiceInstance struct {
	ID      string
	Host    string
	Port    int
	Healthy bool
	Weight  int
}

// NewServiceLoadBalancer creates a new load balancer.
func NewServiceLoadBalancer() *ServiceLoadBalancer {
	return &ServiceLoadBalancer{
		instances: []ServiceInstance{},
		current:   0,
	}
}

// AddInstance adds a service instance to the load balancer.
func (slb *ServiceLoadBalancer) AddInstance(instance ServiceInstance) {
	slb.instances = append(slb.instances, instance)
}

// GetNextInstance returns the next available instance using round-robin.
func (slb *ServiceLoadBalancer) GetNextInstance() (ServiceInstance, error) {
	var healthy []ServiceInstance

	for _, instance := range slb.instances {
		if instance.Healthy {
			healthy = append(healthy, instance)
		}
	}

	if len(healthy) == 0 {
		return ServiceInstance{}, fmt.Errorf("no healthy instances available")
	}

	instance := healthy[slb.current%len(healthy)]
	slb.current++

	return instance, nil
}

// MarkUnhealthy marks an instance as unhealthy.
func (slb *ServiceLoadBalancer) MarkUnhealthy(instanceID string) {
	for i, instance := range slb.instances {
		if instance.ID == instanceID {
			slb.instances[i].Healthy = false
			break
		}
	}
}

// MarkHealthy marks an instance as healthy.
func (slb *ServiceLoadBalancer) MarkHealthy(instanceID string) {
	for i, instance := range slb.instances {
		if instance.ID == instanceID {
			slb.instances[i].Healthy = true
			break
		}
	}
}

// DockerfileGenerator generates a production-ready Dockerfile.
func GenerateDockerfile(config *Config, outputPath string) error {
	dockerfile := fmt.Sprintf(`FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .

RUN go build -ldflags="-s -w" -o cadre ./cmd/cadre

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/cadre /app/cadre

EXPOSE %d

ENV PORT=%d
ENV LOG_FORMAT=json
ENV ENVIRONMENT=production

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:%d/health || exit 1

CMD ["./cadre"]
`, config.Port, config.Port, config.Port)

	return os.WriteFile(outputPath, []byte(dockerfile), 0644)
}

// KubernetesDeploymentGenerator generates a K8s Deployment manifest.
func GenerateKubernetesDeployment(config *Config, deployment *DeploymentInfo, outputPath string) error {
	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: cadre
  labels:
    app: cadre
    version: %s
spec:
  replicas: 3
  selector:
    matchLabels:
      app: cadre
  template:
    metadata:
      labels:
        app: cadre
        version: %s
    spec:
      containers:
      - name: cadre
        image: %s
        ports:
        - containerPort: %d
          name: http
        env:
        - name: PORT
          value: "%d"
        - name: ENVIRONMENT
          value: "%s"
        - name: LOG_FORMAT
          value: "json"
        - name: MAX_AGENTS
          value: "%d"
        livenessProbe:
          httpGet:
            path: /health
            port: %d
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: %d
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        securityContext:
          readOnlyRootFilesystem: true
          runAsNonRoot: true
          runAsUser: 1000
        volumeMounts:
        - name: tmp
          mountPath: /tmp
        - name: logs
          mountPath: /var/log/cadre
      volumes:
      - name: tmp
        emptyDir: {}
      - name: logs
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: cadre
  labels:
    app: cadre
spec:
  type: ClusterIP
  ports:
  - port: 80
    targetPort: %d
    protocol: TCP
    name: http
  selector:
    app: cadre
`, deployment.Version, deployment.Version, deployment.DockerImage,
		config.Port, config.Port, config.Environment, config.MaxAgents,
		config.Port, config.Port, config.Port)

	return os.WriteFile(outputPath, []byte(manifest), 0644)
}

// DockerComposeGenerator generates a docker-compose.yml for development.
func GenerateDockerCompose(config *Config, outputPath string) error {
	compose := fmt.Sprintf(`version: '3.8'

services:
  cadre:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "%d:%d"
    environment:
      ENVIRONMENT: development
      LOG_FORMAT: json
      LOG_LEVEL: debug
      CACHE_ENABLED: "true"
      RATE_LIMIT_ENABLED: "true"
    volumes:
      - ./logs:/var/log/cadre
      - ./config:/etc/cadre/config
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:%d/health"]
      interval: 30s
      timeout: 3s
      retries: 3
      start_period: 5s

volumes:
  logs:
  config:
`, config.Port, config.Port, config.Port)

	return os.WriteFile(outputPath, []byte(compose), 0644)
}

// Helper functions

func ensurePathExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to create the directory
			return os.MkdirAll(path, 0755)
		}

		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	return nil
}

// ConfigurationFileGenerator generates configuration files for deployment.
func GenerateConfigurationFile(config *Config, outputPath string) error {
	content := fmt.Sprintf(`# Production Configuration File
# Generated at: %s

[server]
port = %d
host = "%s"
read_timeout = "%v"
write_timeout = "%v"
shutdown_timeout = "%v"

[logging]
level = "%s"
format = "%s"
output = "%s"

[cache]
enabled = %v
size = %d
ttl = "%v"

[rate_limiting]
enabled = %v
rps = %f
quota_window = "%v"

[agents]
max_agents = %d
timeout = "%v"

[security]
require_auth = %v
api_key_env = "%s"

[health]
health_check_path = "%s"
ready_check_path = "%s"

[environment]
environment = "%s"
version = "%s"
`,
		time.Now().Format("2006-01-02 15:04:05"),
		config.Port,
		config.Host,
		config.ReadTimeout,
		config.WriteTimeout,
		config.ShutdownTimeout,
		config.LogLevel,
		config.LogFormat,
		config.LogOutput,
		config.CacheEnabled,
		config.CacheSize,
		config.CacheTTL,
		config.RateLimitEnabled,
		config.RateLimitRPS,
		config.QuotaWindow,
		config.MaxAgents,
		config.AgentTimeout,
		config.RequireAuth,
		config.APIKeyEnv,
		config.HealthCheckPath,
		config.ReadyCheckPath,
		config.Environment,
		config.Version,
	)

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	return os.WriteFile(outputPath, []byte(content), 0600)
}
