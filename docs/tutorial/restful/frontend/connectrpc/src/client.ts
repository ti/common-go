/**
 * client.ts — ConnectRPC client using plain fetch().
 *
 * The Connect protocol for unary RPCs:
 *   POST /<package>.<Service>/<Method>
 *   Content-Type: application/json
 *   Body: JSON-encoded request message
 *
 * No code generation or special transport library needed.
 */

// Base URL — Vite proxies /pb.UserService/* to the backend (see vite.config.ts).
const BASE_URL: string =
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (import.meta as unknown as { env?: { VITE_API_BASE_URL?: string } }).env
    ?.VITE_API_BASE_URL ?? window.location.origin;

// ── Proto types ───────────────────────────────────────────────────────────────

export interface CreateUserRequest {
  name: string;
  email: string;
  age?: { value: number };
  is_premium?: { value: boolean };
  phone_number?: { value: string };
}

export interface GetUserRequest {
  user_id: string;
}

export interface PageQueryRequest {
  page?: number;
  limit?: number;
  sort?: string[];
}

export interface DeleteUserRequest {
  user_id: string;
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

// ── RPC helper ────────────────────────────────────────────────────────────────

interface ConnectError {
  code: string;
  message: string;
}

async function rpc<Req, Resp>(method: string, req: Req): Promise<Resp> {
  const url = `${BASE_URL}/pb.UserService/${method}`;
  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });

  const body = (await resp.json()) as Resp | ConnectError;

  if (!resp.ok || (body as ConnectError).code) {
    const err = body as ConnectError;
    throw new Error(
      `[${err.code ?? resp.status}] ${err.message ?? resp.statusText}`
    );
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

export { BASE_URL };
