/**
 * client.ts — a typed Connect client for example.user.v1.UserService.
 *
 * Messages and the service descriptor are generated from the proto
 * (`cd examples && buf generate` writes src/gen). @connectrpc/connect-web
 * speaks the Connect protocol over fetch; GetUser and ListUsers are declared
 * side-effect free, so with useHttpGet they are sent as cacheable GET requests.
 */
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { UserService } from "./gen/example/user/v1/user_pb";

// In development Vite proxies /example.user.v1.UserService/* to the Go server
// (vite.config.ts); in production set VITE_API_BASE_URL or serve same-origin.
export const BASE_URL: string = import.meta.env.VITE_API_BASE_URL ?? window.location.origin;

export const userClient = createClient(
  UserService,
  createConnectTransport({ baseUrl: BASE_URL, useHttpGet: true }),
);
