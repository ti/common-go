/**
 * client.ts — ConnectRPC transport client using Connect protocol (JSON over HTTP).
 *
 * The Connect protocol for unary RPCs is simply:
 *   POST /<package>.<Service>/<Method>
 *   Content-Type: application/connect+json
 *   Body: JSON-encoded request message
 *
 * Responses are JSON-encoded, with errors signaled via HTTP status codes and
 * a JSON error body: { "code": "not_found", "message": "..." }
 *
 * This client uses plain fetch() to call the ConnectRPC backend, which means:
 *   - No code generation needed
 *   - Works in all browsers over HTTP/1.1
 *   - Human-readable in DevTools network panel
 *   - Type-safe via TypeScript interfaces
 *
 * For production use, generate a typed client with buf generate + @connectrpc/connect-web.
 * See buf.yaml and buf.gen.yaml for the code generation configuration.
 */

// Base URL for the backend.
// In development: Vite proxies /pb.UserService/* → http://localhost:8080 (see vite.config.ts).
// In production: set VITE_API_BASE_URL environment variable at build time.
const BASE_URL: string =
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (import.meta as unknown as { env?: { VITE_API_BASE_URL?: string } }).env
    ?.VITE_API_BASE_URL ?? window.location.origin;

// ConnectRPC error structure (Connect protocol JSON error body)
export interface ConnectError {
  code: string;     // e.g. "not_found", "invalid_argument", "unauthenticated"
  message: string;
  details?: unknown[];
}

// ── Proto message types ───────────────────────────────────────────────────────

export interface CreateUserRequest {
  name: string;
  email: string;
  age?: { value: number };
  is_premium?: { value: boolean };
  phone_number?: { value: string };
}

export interface GetUserRequest {
  user_id: string; // int64 as string in JSON
}

export interface PageQueryRequest {
  page?: number;
  limit?: number;
  sort?: string[];
}

export interface DeleteUserRequest {
  user_id: string; // int64 as string in JSON
}

export interface User {
  user_id: string;
  name: string;
  email: string;
  created_at?: string;
  updated_at?: string;
  age?: { value: number };
  is_active?: { value: boolean };
  is_premium?: { value: boolean };
  phone_number?: { value: string };
}

export interface UserResponse {
  user?: User;
}

export interface DeleteUserResponse {
  success: boolean;
  message: string;
}

export interface PageUsersResponse {
  data?: User[];
  total?: string;
}

// ── Core RPC helper ───────────────────────────────────────────────────────────

/**
 * Calls a ConnectRPC method using the Connect protocol (application/connect+json).
 *
 * The Connect protocol spec for unary calls:
 *   - Method: POST
 *   - Path:   /<package>.<Service>/<Method>
 *   - Request Content-Type: application/connect+json
 *   - Request body: JSON-encoded proto message
 *   - Response body: JSON-encoded proto message (success) OR connect error (failure)
 *   - Error indicated by HTTP status code != 200 and body: {"code":"...","message":"..."}
 */
async function rpc<Req, Resp>(method: string, req: Req): Promise<Resp> {
  const url = `${BASE_URL}/pb.UserService/${method}`;
  const resp = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/connect+json",
      "Connect-Protocol-Version": "1",
    },
    body: JSON.stringify(req),
  });

  const body = await resp.json() as Resp | ConnectError;

  if (!resp.ok || (body as ConnectError).code) {
    const err = body as ConnectError;
    throw new Error(`[${err.code ?? resp.status}] ${err.message ?? resp.statusText}`);
  }

  return body as Resp;
}

// ── UserService client ────────────────────────────────────────────────────────

export const userClient = {
  createUser: (req: CreateUserRequest): Promise<UserResponse> =>
    rpc("CreateUser", req),

  getUser: (req: GetUserRequest): Promise<UserResponse> =>
    rpc("GetUser", req),

  listUsers: (req: PageQueryRequest): Promise<PageUsersResponse> =>
    rpc("ListUsers", req),

  deleteUser: (req: DeleteUserRequest): Promise<DeleteUserResponse> =>
    rpc("DeleteUser", req),
};

// ── Exported info ─────────────────────────────────────────────────────────────

export { BASE_URL };
