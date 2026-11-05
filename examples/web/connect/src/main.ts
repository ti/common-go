/**
 * main.ts — ConnectRPC User Service Demo
 *
 * Pure TypeScript UI, no framework. Demonstrates calling a ConnectRPC backend
 * (running with grpcmux in h2c mode) from a browser using the Connect protocol.
 *
 * The same Go implementation answers, on one server:
 *   - Connect (this demo) and gRPC-Web → /example.user.v1.UserService/<Method> on :8080
 *   - REST (grpc-gateway)              → /v1/users/** on :8080
 *   - native gRPC                      → :8081
 */

import { userClient, BASE_URL } from "./client";

// ── Helpers ───────────────────────────────────────────────────────────────────

function $(id: string): HTMLElement {
  const el = document.getElementById(id);
  if (!el) throw new Error(`Element #${id} not found`);
  return el;
}

function logResult(panelId: string, label: string, data: unknown, isError = false): void {
  const panel = $(panelId);
  const ts = new Date().toLocaleTimeString();
  // Messages contain bigint fields (timestamps); print them as strings.
  const text =
    typeof data === "string"
      ? data
      : JSON.stringify(data, (_k, v) => (typeof v === "bigint" ? v.toString() : v), 2);
  const color = isError ? "#ff6b6b" : "#69db7c";
  panel.innerHTML =
    `<span style="color:#868e96">[${ts}] ${label}</span>\n` +
    `<span style="color:${color}">${escapeHtml(text)}</span>`;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function setLoading(btnId: string, loading: boolean): void {
  const btn = $(btnId) as HTMLButtonElement;
  btn.disabled = loading;
  const label = btn.dataset.label;
  btn.textContent = loading ? "Loading…" : (label ?? btn.textContent);
}

// ── 1. Create User ────────────────────────────────────────────────────────────

const btnCreate = $("btn-create") as HTMLButtonElement;
btnCreate.dataset.label = btnCreate.textContent ?? "Create User";

btnCreate.addEventListener("click", async () => {
  setLoading("btn-create", true);
  const name = ($("input-name") as HTMLInputElement).value.trim();
  const email = ($("input-email") as HTMLInputElement).value.trim();

  if (!name || !email) {
    logResult("result-create", "Validation", "Name and email are required", true);
    setLoading("btn-create", false);
    return;
  }

  try {
    const resp = await userClient.createUser({ user: { name, email } });
    logResult("result-create", "CreateUser ✓", resp);
    // Pre-fill the get/delete fields with the new user's ID for convenience
    if (resp.user?.userId) {
      ($("input-user-id") as HTMLInputElement).value = resp.user.userId;
      ($("input-delete-id") as HTMLInputElement).value = resp.user.userId;
    }
  } catch (err) {
    logResult("result-create", "CreateUser ✗", String(err), true);
  } finally {
    setLoading("btn-create", false);
  }
});

// ── 2. Get User ───────────────────────────────────────────────────────────────

const btnGet = $("btn-get") as HTMLButtonElement;
btnGet.dataset.label = btnGet.textContent ?? "Get User";

btnGet.addEventListener("click", async () => {
  setLoading("btn-get", true);
  const userId = ($("input-user-id") as HTMLInputElement).value.trim();

  if (!userId) {
    logResult("result-get", "Validation", "User ID is required", true);
    setLoading("btn-get", false);
    return;
  }

  try {
    const resp = await userClient.getUser({ userId });
    logResult("result-get", "GetUser ✓", resp);
  } catch (err) {
    logResult("result-get", "GetUser ✗", String(err), true);
  } finally {
    setLoading("btn-get", false);
  }
});

// ── 3. List Users ─────────────────────────────────────────────────────────────

const btnList = $("btn-list") as HTMLButtonElement;
btnList.dataset.label = btnList.textContent ?? "List Users";

btnList.addEventListener("click", async () => {
  setLoading("btn-list", true);
  const pageToken = ($("input-page") as HTMLInputElement).value.trim();
  const pageSize = parseInt(($("input-limit") as HTMLInputElement).value) || 10;

  try {
    const resp = await userClient.listUsers({ pageSize, pageToken });
    // Put the cursor in the field so the next click fetches the next page.
    ($("input-page") as HTMLInputElement).value = resp.nextPageToken;
    logResult("result-list", `ListUsers ✓ (${resp.users.length} users)`, resp);
  } catch (err) {
    logResult("result-list", "ListUsers ✗", String(err), true);
  } finally {
    setLoading("btn-list", false);
  }
});

// ── 4. Delete User ────────────────────────────────────────────────────────────

const btnDelete = $("btn-delete") as HTMLButtonElement;
btnDelete.dataset.label = btnDelete.textContent ?? "Delete User";

btnDelete.addEventListener("click", async () => {
  setLoading("btn-delete", true);
  const userId = ($("input-delete-id") as HTMLInputElement).value.trim();

  if (!userId) {
    logResult("result-delete", "Validation", "User ID is required", true);
    setLoading("btn-delete", false);
    return;
  }

  try {
    const resp = await userClient.deleteUser({ userId });
    logResult("result-delete", `DeleteUser ✓`, resp);
  } catch (err) {
    logResult("result-delete", "DeleteUser ✗", String(err), true);
  } finally {
    setLoading("btn-delete", false);
  }
});

// ── Protocol info panel ───────────────────────────────────────────────────────

$("info-base-url").textContent = BASE_URL;
$("info-protocol").textContent =
  "Connect protocol (JSON over fetch; GET for side-effect-free methods)";
$("info-server").textContent =
  "grpcmux — Connect / gRPC-Web + REST + native gRPC, one implementation";
