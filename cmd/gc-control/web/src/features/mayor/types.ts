import type { MayorState } from "@/lib/api";

export type MayorFailureState = Extract<MayorState, "missing" | "ambiguous" | "disconnected">;
export type MayorMutation = "send" | "respond";
