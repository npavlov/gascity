import type { ConvoyDetail } from "@/lib/api";
import { Badge, Stack, Text } from "@/ui";

import "./BeadList.css";

export function BeadList({ beads }: { beads: ConvoyDetail["beads"] }) {
  if (!beads?.length) return <Text variant="caption">No tracked beads were returned.</Text>;
  return (
    <Stack className="bead-list" gap="2" aria-label="Tracked beads">
      {beads.map((bead) => (
        <div className="bead-list__row" key={bead.id}>
          <Stack gap="1">
            <Text variant="label">{bead.step_ref || bead.title}</Text>
            <Text variant="code">{bead.id}</Text>
          </Stack>
          <Badge tone={bead.status === "closed" ? "success" : bead.status === "in_progress" ? "info" : "neutral"}>
            {bead.status}
          </Badge>
        </div>
      ))}
    </Stack>
  );
}
