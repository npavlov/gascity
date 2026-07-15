import type {
  MayorEvent,
  MayorState,
  MayorView,
  MessageReceipt,
  PendingInteraction,
  TranscriptPage,
  TranscriptTurn,
} from "@/lib/api";

export type MayorFailureState = Extract<MayorState, "missing" | "ambiguous" | "disconnected">;
export type MayorMutation = "send" | "respond";

export interface MayorSnapshot {
  view: MayorView | null;
  transcript: TranscriptPage | null;
  turns: TranscriptTurn[];
  pending: PendingInteraction | null;
}

export interface MayorLiveState {
  stale: boolean;
  liveTurn: TranscriptTurn | null;
  receipt: MessageReceipt | null;
  event: MayorEvent | null;
}
