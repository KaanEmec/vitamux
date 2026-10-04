import { test } from "node:test";
import assert from "node:assert/strict";
import { start, json, bootstrap, authResult, creds, SECRET, PASSWORD, CODE, ACCESS, REFRESH, USER_ID } from "./helpers.js";

const cognitoError = (type) => json({ __type: `com.amazon#${type}`, message: "boom" }, 400);

test("healthz is open, everything else needs the bearer secret", async (t) => {
  const s = await start({});
  t.after(s.stop);
  assert.equal((await s.call("GET", "/healthz", undefined, {})).status, 200);
  for (const headers of [{}, { authorization: "Bearer nope" }]) {
    const r = await s.call("GET", "/v1/describe", undefined, headers);
    assert.equal(r.status, 401);
    assert.equal(r.type, "application/problem+json");
    assert.equal(r.protocol, "vitamux-connector/1");
    assert.equal(r.json().code, "permanent");
  }
});

test("describe", async (t) => {
  const s = await start({});
  t.after(s.stop);
  const d = (await s.call("GET", "/v1/describe")).json();
  assert.equal(d.protocol, "vitamux-connector/1");
  assert.equal(d.provider, "whoop");
  assert.equal(d.name, "WHOOP (unofficial)");
  assert.equal(d.official, false);
  assert.equal(d.auth_kind, "interactive_mfa");
  assert.equal(d.upstream.package, "@dofek/whoop");
  assert.equal(d.upstream.version, "0.1.65");
  assert.deepEqual(d.streams.map((x) => x.name), ["whoop.heart_rate", "whoop.cycles", "whoop.sleep", "whoop.workouts", "whoop.strain_deep_dive"]);
  assert.deepEqual(d.streams[0], { name: "whoop.heart_rate", interval_s: 3600, lookback_s: 172800, unit_size_s: 604800, max_backfill_s: 7776000 });
  assert.deepEqual(d.rate_limits, [{ requests: 1, per_s: 1 }]);
  assert.deepEqual(d.capabilities, { incremental: true, backfill: true, manual_sync: true });
});

const BEGIN = { redirect_url: "http://localhost:8080/cb", state: "s" };
const login = (password = PASSWORD) => ({ redirect_url: BEGIN.redirect_url, values: { username: "a@example.com", password } });

test("begin asks for the sign-in without calling WHOOP", async (t) => {
  const s = await start({});
  t.after(s.stop);
  const r = (await s.call("POST", "/v1/auth/begin", BEGIN)).json();
  assert.deepEqual(r.step.prompt.fields.map((f) => [f.name, f.kind]), [["username", "text"], ["password", "password"]]);
  assert.equal(r.step.session, undefined);
  assert.equal(s.calls.length, 0);
});

test("app-MFA sign-in", async (t) => {
  const s = await start({
    InitiateAuth: () => json({ ChallengeName: "SOFTWARE_TOKEN_MFA", Session: "cognito-session" }),
    RespondToAuthChallenge: () => authResult(),
    "/users-service/v2/bootstrap/": bootstrap,
  });
  t.after(s.stop);
  const b = (await s.call("POST", "/v1/auth/continue", login())).json();
  assert.deepEqual(b.step.prompt.fields, [{ name: "code", label: "Verification code", kind: "code" }]);
  assert.match(b.step.session, /^[A-Za-z0-9+/]+=*$/); // standard base64
  const r = (await s.call("POST", "/v1/auth/continue", { redirect_url: BEGIN.redirect_url, session: b.step.session, values: { code: CODE } })).json();
  assert.equal(r.authorized.account_id, String(USER_ID));
  const c = r.authorized.credentials;
  assert.equal(c.access_token, ACCESS);
  assert.equal(c.refresh_token, REFRESH);
  assert.deepEqual(c.extra, { user_id: USER_ID });
  assert.ok(Date.parse(c.expires_at) > Date.now());
  const verify = s.calls.find((x) => x.key === "RespondToAuthChallenge").body;
  assert.equal(verify.ChallengeName, "SOFTWARE_TOKEN_MFA");
  assert.equal(verify.Session, "cognito-session");
  assert.equal(verify.ChallengeResponses.SOFTWARE_TOKEN_MFA_CODE, CODE);
  assert.equal(verify.ChallengeResponses.USERNAME, "a@example.com");
  // Secrets never reach the logs.
  for (const secret of [PASSWORD, CODE, ACCESS, REFRESH, "cognito-session", b.step.session]) assert.ok(!s.logs.join("\n").includes(secret));
});

test("SMS-MFA sign-in", async (t) => {
  const s = await start({
    InitiateAuth: () => json({ ChallengeName: "SMS_MFA", Session: "sms-session" }),
    RespondToAuthChallenge: () => authResult(),
    "/users-service/v2/bootstrap/": bootstrap,
  });
  t.after(s.stop);
  const b = (await s.call("POST", "/v1/auth/continue", login())).json();
  assert.match(b.step.prompt.message, /SMS/);
  const r = (await s.call("POST", "/v1/auth/continue", { redirect_url: BEGIN.redirect_url, session: b.step.session, values: { code: CODE } })).json();
  assert.equal(r.authorized.account_id, String(USER_ID));
  assert.equal(s.calls.find((x) => x.key === "RespondToAuthChallenge").body.ChallengeResponses.SMS_MFA_CODE, CODE);
});

test("email-code sign-in answers EMAIL_OTP", async (t) => {
  const s = await start({
    InitiateAuth: () => json({ ChallengeName: "EMAIL_OTP", Session: "email-session" }),
    RespondToAuthChallenge: (c) => (c.body.ChallengeName === "EMAIL_OTP" && c.body.ChallengeResponses.EMAIL_OTP_CODE === CODE ? authResult() : cognitoError("CodeMismatchException")),
    "/users-service/v2/bootstrap/": bootstrap,
  });
  t.after(s.stop);
  const b = (await s.call("POST", "/v1/auth/continue", login())).json();
  assert.match(b.step.prompt.message, /emailed/);
  const r = (await s.call("POST", "/v1/auth/continue", { redirect_url: BEGIN.redirect_url, session: b.step.session, values: { code: CODE } })).json();
  assert.equal(r.authorized.account_id, String(USER_ID));
  assert.deepEqual(s.calls.find((x) => x.key === "RespondToAuthChallenge").body.ChallengeResponses, { USERNAME: "a@example.com", EMAIL_OTP_CODE: CODE });
});

test("sign-in without MFA is authorized at once", async (t) => {
  const s = await start({ InitiateAuth: () => authResult(), "/users-service/v2/bootstrap/": bootstrap });
  t.after(s.stop);
  const r = (await s.call("POST", "/v1/auth/continue", login())).json();
  assert.equal(r.authorized.account_id, String(USER_ID));
});

test("wrong code and wrong password are reauth_required without echoing anything", async (t) => {
  const s = await start({
    InitiateAuth: (c) => (c.body.AuthParameters.PASSWORD === PASSWORD ? json({ ChallengeName: "SMS_MFA", Session: "x" }) : cognitoError("NotAuthorizedException")),
    RespondToAuthChallenge: () => cognitoError("CodeMismatchException"),
  });
  t.after(s.stop);
  const bad = await s.call("POST", "/v1/auth/continue", login("wrong"));
  assert.equal(bad.status, 401);
  assert.equal(bad.json().code, "reauth_required");
  const b = (await s.call("POST", "/v1/auth/continue", login())).json();
  const r = await s.call("POST", "/v1/auth/continue", { redirect_url: BEGIN.redirect_url, session: b.step.session, values: { code: CODE } });
  assert.equal(r.status, 401);
  assert.equal(r.type, "application/problem+json");
  assert.equal(r.json().code, "reauth_required");
  assert.ok(!r.text.includes(CODE) && !r.text.includes(b.step.session));
  const garbage = await s.call("POST", "/v1/auth/continue", { redirect_url: BEGIN.redirect_url, session: "Z2FyYmFnZQ==", values: { code: CODE } });
  assert.equal(garbage.json().code, "permanent");
});

test("refresh keeps the refresh token unless Cognito rotates it", async (t) => {
  let rotate = true;
  const s = await start({
    InitiateAuth: () => authResult(rotate ? { RefreshToken: "rotated-refresh-token", AccessToken: "new-access" } : { RefreshToken: undefined, AccessToken: "new-access" }),
    "/users-service/v2/bootstrap/": () => json({}, 500), // user id is best effort: stored one is kept
  });
  t.after(s.stop);
  let r = (await s.call("POST", "/v1/auth/refresh", { credentials: creds() })).json();
  let c = r.credentials;
  assert.equal(c.refresh_token, "rotated-refresh-token");
  assert.equal(c.access_token, "new-access");
  assert.deepEqual(c.extra, { user_id: USER_ID });
  assert.equal(s.calls.find((x) => x.key === "InitiateAuth").body.AuthFlow, "REFRESH_TOKEN_AUTH");
  rotate = false;
  r = (await s.call("POST", "/v1/auth/refresh", { credentials: creds() })).json();
  assert.equal(r.credentials.refresh_token, REFRESH);
});

test("a refused refresh token is reauth_required", async (t) => {
  const s = await start({ InitiateAuth: () => cognitoError("NotAuthorizedException") });
  t.after(s.stop);
  const r = await s.call("POST", "/v1/auth/refresh", { credentials: creds() });
  assert.equal(r.status, 401);
  assert.equal(r.json().code, "reauth_required");
  assert.equal((await s.call("POST", "/v1/auth/refresh", { credentials: { access_token: "x" } })).json().code, "permanent");
});

test("Cognito throttling and outages", async (t) => {
  let type = "LimitExceededException";
  const s = await start({ InitiateAuth: () => cognitoError(type) });
  t.after(s.stop);
  assert.equal((await s.call("POST", "/v1/auth/continue", login())).json().code, "rate_limited");
  type = "InternalErrorException";
  assert.equal((await s.call("POST", "/v1/auth/continue", login())).json().code, "transient");
  type = "SomethingNew";
  assert.equal((await s.call("POST", "/v1/auth/continue", login())).json().code, "permanent");
});
