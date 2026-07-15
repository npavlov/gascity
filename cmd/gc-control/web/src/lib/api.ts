import createClient from "openapi-fetch";

import type { components, paths } from "@/generated/schema";

export type Health = components["schemas"]["HealthBody"];
export type Problem = components["schemas"]["Problem"];
export type ConvoySummary = components["schemas"]["ConvoySummary"];
export type ConvoyDetail = components["schemas"]["ConvoyDetail"];
export type ConvoyList = components["schemas"]["ResourceListConvoySummary"];
export type BeadList = components["schemas"]["ResourceListBeadView"];
export type Order = components["schemas"]["OrderView"];
export type OrderList = components["schemas"]["ResourceListOrderView"];
export type OrderRun = components["schemas"]["OrderRunView"];
export type OrderRunList = components["schemas"]["ResourceListOrderRunView"];
export type OrderRunOutput = components["schemas"]["OrderRunOutput"];
export type MayorView = components["schemas"]["MayorView"];
export type MayorState = MayorView["state"];
export type MayorProblem = components["schemas"]["MayorProblem"];
export type MayorEvent = components["schemas"]["MayorEvent"];
export type TranscriptPage = components["schemas"]["TranscriptPage"];
export type TranscriptTurn = components["schemas"]["TranscriptTurn"];
export type PendingInteraction = components["schemas"]["PendingInteraction"];
export type MessageReceipt = components["schemas"]["MessageReceipt"];
export type InteractionReceipt = components["schemas"]["InteractionReceipt"];

export interface HealthAPI {
  health(signal?: AbortSignal): Promise<Health>;
}

export interface ConvoysAPI {
  listConvoys(signal?: AbortSignal): Promise<ConvoyList>;
  getConvoy(id: string, signal?: AbortSignal): Promise<ConvoyDetail>;
  listBeads(id: string, signal?: AbortSignal): Promise<BeadList>;
}

export interface OrdersAPI {
  listOrders(signal?: AbortSignal): Promise<OrderList>;
  listOrderHistory(scopedName: string, before?: string, limit?: number, signal?: AbortSignal): Promise<OrderRunList>;
  getOrderRunOutput(beadID: string, storeRef: string, signal?: AbortSignal): Promise<OrderRunOutput>;
}

export interface MayorAPI {
  getMayor(signal?: AbortSignal): Promise<MayorView>;
  getMayorTranscript(before?: string, signal?: AbortSignal): Promise<TranscriptPage>;
  sendMayorMessage(message: string): Promise<MessageReceipt>;
  respondMayorInteraction(requestID: string, action: string, text?: string, metadata?: Record<string, string>): Promise<InteractionReceipt>;
}

// Feature methods remain optional at the composition boundary so health-only
// embedders and tests do not need unrelated fakes. The production export below
// implements every facet.
export type ControlCenterAPI = HealthAPI & Partial<ConvoysAPI & OrdersAPI & MayorAPI>;
export type LiveControlCenterAPI = HealthAPI & ConvoysAPI & OrdersAPI & MayorAPI;

const mayorProblemTypePrefix = "urn:gascity:control-center:mayor:";

export class MayorRequestError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, status: number, message: string) {
    super(message);
    this.name = "MayorRequestError";
    this.code = code;
    this.status = status;
  }
}

export interface ControlCenterClientOptions {
  baseUrl?: string;
  fetch?: typeof globalThis.fetch;
}

export function createControlCenterAPI(options: ControlCenterClientOptions = {}): LiveControlCenterAPI {
  const client = createClient<paths>({
    baseUrl: options.baseUrl ?? "",
    ...(options.fetch ? { fetch: options.fetch } : {}),
  });

  return {
    async health(signal) {
      const { data, error, response } = await client.GET("/api/v1/health", { signal });
      if (!response.ok || error || !data) {
        throw new Error(`health request failed with status ${response.status}`);
      }
      return data;
    },
    async listConvoys(signal) {
      const { data, error, response } = await client.GET("/api/v1/convoys", { signal });
      if (!response.ok || error || !data) throw new Error(`convoy list request failed with status ${response.status}`);
      return data;
    },
    async getConvoy(id, signal) {
      const { data, error, response } = await client.GET("/api/v1/convoys/{id}", { params: { path: { id } }, signal });
      if (!response.ok || error || !data) throw new Error(`convoy detail request failed with status ${response.status}`);
      return data;
    },
    async listBeads(id, signal) {
      const { data, error, response } = await client.GET("/api/v1/convoys/{id}/beads", { params: { path: { id } }, signal });
      if (!response.ok || error || !data) throw new Error(`bead list request failed with status ${response.status}`);
      return data;
    },
    async listOrders(signal) {
      const { data, error, response } = await client.GET("/api/v1/orders", { signal });
      if (!response.ok || error || !data) throw new Error(`order list request failed with status ${response.status}`);
      return data;
    },
    async listOrderHistory(scopedName, before, limit = 20, signal) {
      const { data, error, response } = await client.GET("/api/v1/orders/history", {
        params: { query: { scoped_name: scopedName, before, limit } },
        signal,
      });
      if (!response.ok || error || !data) throw new Error(`order history request failed with status ${response.status}`);
      return data;
    },
    async getOrderRunOutput(beadID, storeRef, signal) {
      const { data, error, response } = await client.GET("/api/v1/orders/history/{bead_id}", {
        params: { path: { bead_id: beadID }, query: { store_ref: storeRef } },
        signal,
      });
      if (!response.ok || error || !data) throw new Error(`order output request failed with status ${response.status}`);
      return data;
    },
    async getMayor(signal) {
      const { data, error, response } = await client.GET("/api/v1/mayor", { signal });
      if (!response.ok || error || !data) throw mayorRequestError(error, response.status, "Mayor state request failed");
      return data;
    },
    async getMayorTranscript(before, signal) {
      const { data, error, response } = await client.GET("/api/v1/mayor/transcript", {
        params: { query: { before } },
        signal,
      });
      if (!response.ok || error || !data) throw mayorRequestError(error, response.status, "Mayor transcript request failed");
      return data;
    },
    async sendMayorMessage(message) {
      const { data, error, response } = await client.POST("/api/v1/mayor/messages", { body: { message } });
      if (!response.ok || error || !data) throw mayorRequestError(error, response.status, "Mayor message request failed");
      return data;
    },
    async respondMayorInteraction(requestID, action, text, metadata) {
      const { data, error, response } = await client.POST("/api/v1/mayor/interactions/{request_id}", {
        params: { path: { request_id: requestID } },
        body: { request_id: requestID, action, text, metadata },
      });
      if (!response.ok || error || !data) throw mayorRequestError(error, response.status, "Mayor interaction request failed");
      return data;
    },
  };
}

function mayorRequestError(problem: unknown, status: number, fallback: string): MayorRequestError {
  const body = problem && typeof problem === "object" ? problem as { type?: unknown; detail?: unknown } : null;
  const type = typeof body?.type === "string" ? body.type : "";
  const code = type.startsWith(mayorProblemTypePrefix)
    ? type.slice(mayorProblemTypePrefix.length) || "mayor_request_failed"
    : "mayor_request_failed";
  const detail = typeof body?.detail === "string" && body.detail.trim() !== ""
    ? body.detail
    : `${fallback} with status ${status}`;
  return new MayorRequestError(code, status, detail);
}

export const api = createControlCenterAPI();
