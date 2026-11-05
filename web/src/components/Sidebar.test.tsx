// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { View } from "../lib/view";
import { FeatureContext } from "./FeatureContext";
import { Sidebar } from "./Sidebar";

function view(isPublic: boolean, extra: Partial<View> = {}): View {
  return { Public: isPublic, Count: 5, GitHubCount: 0, ...extra } as unknown as View;
}

function renderSidebar(isPublic: boolean) {
  const open = vi.fn();
  const { container } = render(
    <FeatureContext.Provider value={open}>
      <Sidebar v={view(isPublic)} />
    </FeatureContext.Provider>,
  );
  return { open, container };
}

afterEach(cleanup);

describe("Sidebar footer", () => {
  it("offers sign-in at the foot of the sidebar for a guest", () => {
    const { open, container } = renderSidebar(true);
    const signIn = screen.getByRole("button", { name: "Войти" });
    expect(signIn.closest(".sidebar-footer")).not.toBeNull();
    expect(container.querySelector(".sidebar-bottom")).toBeNull();
    fireEvent.click(signIn);
    expect(open).toHaveBeenCalledWith("profile");
  });

  it("keeps the panels and the profile button together for the owner", () => {
    const { open, container } = renderSidebar(false);
    const footer = container.querySelector(".sidebar-footer");
    expect(footer?.querySelector(".sidebar-bottom")).not.toBeNull();
    const profile = screen.getByRole("button", { name: "Профиль" });
    expect(profile.closest(".sidebar-footer")).toBe(footer);
    fireEvent.click(profile);
    expect(open).toHaveBeenCalledWith("profile");
  });

  it("shows when MCP last reached the server instead of an unknown state", () => {
    const { container } = render(
      <FeatureContext.Provider value={vi.fn()}>
        <Sidebar v={view(false, { MCPLastSeen: new Date(Date.now() - 2 * 60000).toISOString() })} />
      </FeatureContext.Provider>,
    );
    expect(container.querySelector(".system")?.textContent).toContain("2 мин назад");
    cleanup();
    const idle = render(<Sidebar v={view(false)} />);
    expect(idle.container.querySelector(".system")?.textContent).toContain("Нет обращений");
  });
});
