import { createRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SearchIcon } from "../icons";
import { Button } from "./Button";
import { Grid } from "./Grid";
import { IconButton } from "./IconButton";
import { Input } from "./Input";
import { Select } from "./Select";
import { Stack } from "./Stack";
import { Text } from "./Text";
import { Textarea } from "./Textarea";
import buttonCSS from "./Button.css?raw";
import inputCSS from "./Input.css?raw";
import selectCSS from "./Select.css?raw";
import textareaCSS from "./Textarea.css?raw";

describe("UI primitives", () => {
  it("Button preserves native behavior, variants, and refs", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const ref = createRef<HTMLButtonElement>();
    const { rerender } = render(
      <Button ref={ref} variant="primary" size="compact" onClick={onClick}>Run</Button>,
    );
    const button = screen.getByRole("button", { name: "Run" });
    expect(button).toHaveAttribute("type", "button");
    expect(button).toHaveAttribute("data-variant", "primary");
    expect(button).toHaveAttribute("data-size", "compact");
    expect(ref.current).toBe(button);
    await user.click(button);
    expect(onClick).toHaveBeenCalledOnce();

    rerender(<Button type="submit" disabled onClick={onClick}>Run</Button>);
    expect(button).toHaveAttribute("type", "submit");
    await user.click(button);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("IconButton keeps its required accessible name and decorative icon", () => {
    const ref = createRef<HTMLButtonElement>();
    render(<IconButton ref={ref} icon={SearchIcon} aria-label="Search" />);
    const button = screen.getByRole("button", { name: "Search" });
    expect(ref.current).toBe(button);
    expect(button.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });

  it("Input preserves native form and accessibility props", () => {
    const ref = createRef<HTMLInputElement>();
    render(<Input ref={ref} name="query" required aria-label="Query" defaultValue="convoy" />);
    const input = screen.getByRole("textbox", { name: "Query" });
    expect(ref.current).toBe(input);
    expect(input).toHaveValue("convoy");
    expect(input).toBeRequired();
    fireEvent.change(input, { target: { value: "order" } });
    expect(input).toHaveValue("order");
  });

  it("Select preserves native options and ref", async () => {
    const user = userEvent.setup();
    const ref = createRef<HTMLSelectElement>();
    render(<Select ref={ref} name="state" aria-label="State"><option>Running</option><option>Stopped</option></Select>);
    const select = screen.getByRole("combobox", { name: "State" });
    expect(ref.current).toBe(select);
    await user.selectOptions(select, "Stopped");
    expect(select).toHaveValue("Stopped");
  });

  it("Textarea preserves validation and ref", () => {
    const ref = createRef<HTMLTextAreaElement>();
    render(<Textarea ref={ref} name="comment" required aria-label="Comment" defaultValue="Fix" />);
    expect(ref.current).toBe(screen.getByRole("textbox", { name: "Comment" }));
    expect(ref.current).toHaveValue("Fix");
    expect(ref.current).toBeRequired();
  });

  it("Text exposes a closed semantic role", () => {
    const ref = createRef<HTMLSpanElement>();
    render(<Text ref={ref} variant="code">ga-123</Text>);
    expect(ref.current).toHaveAttribute("data-variant", "code");
    expect(ref.current).toHaveTextContent("ga-123");
  });

  it("Stack and Grid expose token-backed gaps and forward refs", () => {
    const stackRef = createRef<HTMLDivElement>();
    const gridRef = createRef<HTMLDivElement>();
    render(<><Stack ref={stackRef} gap="3">Stack</Stack><Grid ref={gridRef} gap="4">Grid</Grid></>);
    expect(stackRef.current).toHaveAttribute("data-gap", "3");
    expect(gridRef.current).toHaveAttribute("data-gap", "4");
  });

  it.each([
    ["button", buttonCSS, ".cc-button:focus-visible"],
    ["input", inputCSS, ".cc-input:focus-visible"],
    ["select", selectCSS, ".cc-select:focus-visible"],
    ["textarea", textareaCSS, ".cc-textarea:focus-visible"],
  ])("defines a token-backed focus-visible rule for %s", (_name, source, selector) => {
    expect(source).toContain(selector);
    expect(source).toMatch(/--cc-(?:color-border-focus|shadow-focus)/);
  });
});
