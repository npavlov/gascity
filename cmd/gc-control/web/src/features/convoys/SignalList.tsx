import type { ConvoySummary } from "@/lib/api";
import {
  AlertIcon,
  MessageIcon,
  PauseIcon,
  PlayIcon,
  StatusSignal,
  StopIcon,
} from "@/ui";
import type { UIIcon } from "@/ui";

const signalIcons: Record<NonNullable<ConvoySummary["signals"]>[number]["key"], UIIcon> = {
  fail_gate: AlertIcon,
  needs_input: MessageIcon,
  running: PlayIcon,
  stopped: StopIcon,
  waiting: PauseIcon,
};

export function SignalList({ signals }: { signals: ConvoySummary["signals"] }) {
  if (!signals?.length) return null;
  return (
    <div className="live-signal-list" aria-label="Convoy status signals">
      {signals.map((signal) => (
        <StatusSignal
          key={signal.key}
          label={signal.label}
          tone={signal.tone}
          icon={signalIcons[signal.key]}
          title={signal.detail}
        />
      ))}
    </div>
  );
}
