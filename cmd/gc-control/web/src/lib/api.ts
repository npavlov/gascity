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
export type MailCount = components["schemas"]["MailCount"];
export type MailMessage = components["schemas"]["MailMessage"];
export type MailPage = components["schemas"]["MailPage"];
export type MailThread = components["schemas"]["MailThread"];

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

export interface MailAPI {
  getMailCount(signal?: AbortSignal): Promise<MailCount>;
  listMail(status: "unread" | "all", cursor?: string, limit?: number, signal?: AbortSignal): Promise<MailPage>;
  getMail(id: string, rig?: string, signal?: AbortSignal): Promise<MailMessage>;
  getMailThread(idOrThreadID: string, rig?: string, signal?: AbortSignal): Promise<MailThread>;
}

export class ControlCenterAPIError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = "ControlCenterAPIError";
  }
}

// Feature methods remain optional at the composition boundary so health-only
// embedders and tests do not need unrelated fakes. The production export below
// implements every facet.
export type ControlCenterAPI = HealthAPI & Partial<ConvoysAPI & OrdersAPI & MailAPI>;
export type LiveControlCenterAPI = HealthAPI & ConvoysAPI & OrdersAPI & MailAPI;

const client = createClient<paths>({ baseUrl: "" });

export const api: LiveControlCenterAPI = {
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
  async getMailCount(signal) {
    const { data, error, response } = await client.GET("/api/v1/mail/count", { signal });
    if (!response.ok || error || !data) throw new ControlCenterAPIError("mail count request failed", response.status);
    return data;
  },
  async listMail(status, cursor, limit = 50, signal) {
    const { data, error, response } = await client.GET("/api/v1/mail", {
      params: { query: { status, cursor, limit } },
      signal,
    });
    if (!response.ok || error || !data) throw new ControlCenterAPIError("mail list request failed", response.status);
    return data;
  },
  async getMail(id, rig, signal) {
    const { data, error, response } = await client.GET("/api/v1/mail/{id}", {
      params: { path: { id }, query: { rig } },
      signal,
    });
    if (!response.ok || error || !data) throw new ControlCenterAPIError("mail detail request failed", response.status);
    return data;
  },
  async getMailThread(idOrThreadID, rig, signal) {
    const { data, error, response } = await client.GET("/api/v1/mail/{id}/thread", {
      params: { path: { id: idOrThreadID }, query: { rig } },
      signal,
    });
    if (!response.ok || error || !data) throw new ControlCenterAPIError("mail thread request failed", response.status);
    return data;
  },
};
