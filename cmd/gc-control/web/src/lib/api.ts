import createClient from "openapi-fetch";

import type { components, paths } from "@/generated/schema";

export type Health = components["schemas"]["HealthBody"];

export interface ControlCenterAPI {
  health(): Promise<Health>;
}

const client = createClient<paths>({ baseUrl: "" });

export const api: ControlCenterAPI = {
  async health() {
    const { data, error, response } = await client.GET("/api/v1/health");
    if (!response.ok || error || !data) {
      throw new Error(`health request failed with status ${response.status}`);
    }
    return data;
  },
};
