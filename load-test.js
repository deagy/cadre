import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { Rate, Trend, Counter, Gauge } from 'k6/metrics';

const baseURL = __ENV.CADRE_URL || 'http://localhost:8080';

// Custom metrics
const errorRate = new Rate('errors');
const healthDuration = new Trend('health_duration');
const readyDuration = new Trend('ready_duration');
const metricsDuration = new Trend('metrics_duration');
const statusDuration = new Trend('status_duration');
const errorCounter = new Counter('error_count');
const requestCounter = new Counter('request_count');
const healthyGauge = new Gauge('health_status');

export const options = {
  stages: [
    // Ramp-up: 0 to 50 VUs over 2 minutes
    { duration: '2m', target: 50 },
    // Stay at 50 VUs for 5 minutes
    { duration: '5m', target: 50 },
    // Spike: 50 to 100 VUs
    { duration: '1m', target: 100 },
    // Spike sustained for 2 minutes
    { duration: '2m', target: 100 },
    // Ramp down: 100 to 0 VUs over 2 minutes
    { duration: '2m', target: 0 },
  ],
  thresholds: {
    'http_req_duration': ['p(95)<500', 'p(99)<1000'],
    'http_req_failed': ['rate<0.1'],
    'errors': ['rate<0.05'],
  },
};

export default function() {
  group('Health Check Endpoint', () => {
    const res = http.get(`${baseURL}/health`);

    requestCounter.add(1);
    healthDuration.add(res.timings.duration);

    const isHealthy = res.status === 200;
    healthyGauge.add(isHealthy ? 1 : 0);

    check(res, {
      'health: status is 200': (r) => r.status === 200,
      'health: response time < 100ms': (r) => r.timings.duration < 100,
      'health: has body': (r) => r.body.length > 0,
      'health: contains status field': (r) => r.body.includes('status'),
    }) || errorRate.add(1);
  });

  sleep(1);

  group('Readiness Probe', () => {
    const res = http.get(`${baseURL}/ready`);

    requestCounter.add(1);
    readyDuration.add(res.timings.duration);

    check(res, {
      'ready: status is 200': (r) => r.status === 200,
      'ready: response time < 50ms': (r) => r.timings.duration < 50,
      'ready: contains status field': (r) => r.body.includes('status'),
    }) || errorRate.add(1);
  });

  sleep(1);

  group('Status Endpoint', () => {
    const res = http.get(`${baseURL}/status`);

    requestCounter.add(1);
    statusDuration.add(res.timings.duration);

    check(res, {
      'status: status is 200': (r) => r.status === 200,
      'status: response time < 100ms': (r) => r.timings.duration < 100,
      'status: has health field': (r) => r.body.includes('health'),
      'status: has ready field': (r) => r.body.includes('ready'),
    }) || errorRate.add(1);
  });

  sleep(1);

  group('Metrics Endpoint', () => {
    const res = http.get(`${baseURL}/metrics`);

    requestCounter.add(1);
    metricsDuration.add(res.timings.duration);

    check(res, {
      'metrics: status is 200': (r) => r.status === 200,
      'metrics: response time < 200ms': (r) => r.timings.duration < 200,
      'metrics: content type is text': (r) =>
        r.headers['Content-Type'].includes('text/plain'),
      'metrics: contains http_requests': (r) => r.body.includes('http_requests'),
    }) || errorRate.add(1);
  });

  sleep(1);

  group('Error Scenarios', () => {
    // Test 404 handling
    const res404 = http.get(`${baseURL}/nonexistent`);
    check(res404, {
      '404: returns 404 status': (r) => r.status === 404,
    }) || errorCounter.add(1);

    // Test 503 when service degraded (if applicable)
    // This would need a way to trigger unhealthy state
  });

  sleep(2);
}

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    '/tmp/summary.json': JSON.stringify(data, null, 2),
  };
}

// Custom summary formatter
function textSummary(data, options) {
  const { indent = '', enableColors = false } = options;
  let summary = '\n';

  summary += `${indent}Performance Summary:\n`;
  summary += `${indent}  Total Requests: ${data.metrics.request_count.value}\n`;
  summary += `${indent}  Total Errors: ${data.metrics.error_count.value}\n`;
  summary += `${indent}  Error Rate: ${(data.metrics.errors.value * 100).toFixed(2)}%\n\n`;

  summary += `${indent}Endpoint Performance:\n`;

  const endpoints = ['health', 'ready', 'status', 'metrics'];
  endpoints.forEach((endpoint) => {
    const key = `${endpoint}_duration`;
    if (data.metrics[key]) {
      const metric = data.metrics[key];
      summary += `${indent}  /${endpoint}:\n`;
      summary += `${indent}    Avg: ${metric.value?.toFixed(2)}ms\n`;
      summary += `${indent}    P95: ${metric.values?.p95?.toFixed(2)}ms\n`;
      summary += `${indent}    P99: ${metric.values?.p99?.toFixed(2)}ms\n`;
    }
  });

  summary += `${indent}SLA Status:\n`;
  const p95_duration = data.metrics.http_req_duration?.values?.['p(95)'] || 0;
  const p99_duration = data.metrics.http_req_duration?.values?.['p(99)'] || 0;
  const errorRate_value = data.metrics.http_req_failed?.value || 0;

  summary += `${indent}  P95 < 500ms: ${p95_duration < 500 ? '✓ PASS' : '✗ FAIL'}\n`;
  summary += `${indent}  P99 < 1000ms: ${p99_duration < 1000 ? '✓ PASS' : '✗ FAIL'}\n`;
  summary += `${indent}  Error Rate < 0.1%: ${errorRate_value < 0.001 ? '✓ PASS' : '✗ FAIL'}\n`;

  return summary;
}
