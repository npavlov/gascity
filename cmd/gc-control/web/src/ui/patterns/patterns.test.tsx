import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Button } from "../primitives/Button";
import { ActionBar } from "./ActionBar";
import { DetailHeader } from "./DetailHeader";
import { ToolFrame } from "./ToolFrame";

describe("domain-neutral UI patterns", () => {
  it("ActionBar is a labelled wrapping toolbar", () => {
    const ref = createRef<HTMLDivElement>();
    render(<ActionBar ref={ref} aria-label="Convoy actions"><Button>Run</Button><Button>Stop</Button></ActionBar>);
    expect(ref.current).toBe(screen.getByRole("toolbar", { name: "Convoy actions" }));
    expect(ref.current).toHaveAttribute("data-wrap", "true");
  });

  it("DetailHeader associates its title and composes status/actions", () => {
    render(<DetailHeader title="Convoy 42" subtitle="feature/payments" status="Running" actions={<Button>Open</Button>} />);
    expect(screen.getByRole("banner")).toHaveAccessibleName("Convoy 42");
    expect(screen.getByText("Running")).toBeVisible();
    expect(screen.getByRole("button", { name: "Open" })).toBeVisible();
  });

  it("ToolFrame composes header, sidebar, main tool, and inspector slots", () => {
    const ref = createRef<HTMLDivElement>();
    render(<ToolFrame ref={ref} header={<div>Header</div>} sidebar={<nav aria-label="Convoys">List</nav>} inspector={<aside>Details</aside>}><div>Diff</div></ToolFrame>);
    expect(ref.current).toHaveAttribute("data-layout", "tool-frame");
    expect(screen.getByRole("navigation", { name: "Convoys" })).toBeVisible();
    expect(screen.getByRole("main")).toHaveTextContent("Diff");
    expect(screen.getByRole("complementary")).toHaveTextContent("Details");
  });
});
