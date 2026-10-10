// k6 smoke test: each virtual user signs in with the development login, browses problems,
// submits an accepted A + B solution and waits for its verdict.
//
//   make seed && make run-api && make run-worker      # in other terminals
//   k6 run loadtest/smoke.js
//   k6 run -e BASE_URL=http://localhost:8080 -e VUS=20 -e DURATION=1m loadtest/smoke.js
//
// The API's submit limits apply per user, so start it with SUBMIT_COOLDOWN=0s for heavier runs.
import http from "k6/http";
import { check, fail, sleep } from "k6";
import { Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

export const options = {
  vus: Number(__ENV.VUS || 5),
  duration: __ENV.DURATION || "30s",
  // Keep each VU signed in across iterations; k6 clears cookies between iterations by default.
  noCookiesReset: true,
  thresholds: {
    checks: ["rate>0.99"],
    http_req_failed: ["rate<0.01"],
    "http_req_duration{rpc:ListProblems}": ["p(95)<200"],
    "http_req_duration{rpc:SubmitSolution}": ["p(95)<300"],
    verdict_seconds: ["p(95)<10"],
  },
};

const verdictSeconds = new Trend("verdict_seconds");

const SOLUTION = `import sys

a, b = map(int, sys.stdin.read().split())
print(a + b)
`;

function rpc(service, method, body) {
  return http.post(`${BASE_URL}/valence.v1.${service}/${method}`, JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    tags: { rpc: method },
  });
}

export default function () {
  // One user per VU, signed in on its first iteration.
  if (__ITER === 0) {
    const login = http.get(`${BASE_URL}/auth/dev?user=k6-vu-${__VU}`, { redirects: 0, tags: { rpc: "DevLogin" } });
    if (!check(login, { "dev login sets a session": (r) => r.status < 400 && r.cookies.valence_session !== undefined })) {
      fail(`dev login returned ${login.status}; is the API running with VALENCE_ENV=dev?`);
    }
  }

  const list = rpc("ProblemService", "ListProblems", { pageSize: 50 });
  check(list, { "problems listed": (r) => r.status === 200 && (r.json("problems") || []).length > 0 });

  const problem = rpc("ProblemService", "GetProblem", { slug: "aplusb" });
  if (!check(problem, { "aplusb exists (run make seed)": (r) => r.status === 200 })) {
    return;
  }

  const submit = rpc("SubmissionService", "SubmitSolution", {
    problemSlug: "aplusb",
    language: "python3",
    source: SOLUTION,
  });
  if (!check(submit, { "submission accepted": (r) => r.status === 200 })) {
    sleep(1);
    return;
  }
  const id = submit.json("submissionId");

  const start = Date.now();
  let submission;
  while (Date.now() - start < 30000) {
    const r = rpc("SubmissionService", "GetSubmission", { submissionId: id });
    submission = r.json("submission");
    if (submission && submission.status === "SUBMISSION_STATUS_FINALIZED") {
      break;
    }
    sleep(0.25);
  }
  verdictSeconds.add((Date.now() - start) / 1000);
  check(submission, { "verdict is AC": (s) => s && s.verdict === "VERDICT_ACCEPTED" });
}
