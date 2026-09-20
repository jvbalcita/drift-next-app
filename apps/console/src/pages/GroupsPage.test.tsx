// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { MockControlPlaneClient } from "@/lib/api/mock-control-plane";
import { Toaster } from "@/components/ui/sonner";
import { GroupsPage } from "./GroupsPage";

function harness() {
  const client = new MockControlPlaneClient();
  const dispatch = async (intent: Parameters<typeof client.dispatch>[0]) =>
    client.dispatch(intent);
  const view = () => (
    <>
      <Toaster />
      <GroupsPage snapshot={client.getSnapshot()} dispatch={dispatch} />
    </>
  );
  return { client, view };
}

describe("GroupsPage", () => {
  it("renders one tabless surface instead of the Groups, Membership and Ordering tabs", () => {
    const { view } = harness();
    render(view());

    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Groups and Membership" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Rack A" })).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Ungrouped" }),
    ).toBeInTheDocument();
  });

  it("shows each group's devices in order with their positions", () => {
    const { view } = harness();
    render(view());

    const rackA = within(screen.getByRole("region", { name: "Rack A" }));
    const rows = rackA.getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("Atlas 04");
    expect(rows[0]).toHaveTextContent("1");
    expect(rows[1]).toHaveTextContent("Atlas 07");
    expect(rows[1]).toHaveTextContent("2");
  });

  it("creates a named group through the control plane", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    expect(screen.queryByLabelText("Group name")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "New group" }));
    const dialog = within(
      screen.getByRole("dialog", { name: "Create device group" }),
    );
    await user.type(dialog.getByLabelText("Group name"), "Rack D");
    await user.click(dialog.getByRole("button", { name: "Create group" }));
    rerender(view());

    expect(
      await screen.findByText(/Device group created/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Rack D" })).toBeInTheDocument();
    expect(
      client.getSnapshot().groups.some((group) => group.name === "Rack D"),
    ).toBe(true);
  });

  it("keeps a duplicate-name error inside the create dialog in human language", async () => {
    const user = userEvent.setup();
    const { view } = harness();
    render(view());

    await user.click(screen.getByRole("button", { name: "New group" }));
    const dialog = within(
      screen.getByRole("dialog", { name: "Create device group" }),
    );
    const input = dialog.getByLabelText("Group name");
    await user.type(input, "Rack A");
    await user.click(dialog.getByRole("button", { name: "Create group" }));

    expect(dialog.getByRole("alert")).toHaveTextContent(
      "A group with this name already exists. Choose a different name.",
    );
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription(
      "A group with this name already exists. Choose a different name.",
    );
    expect(
      screen.queryByText("resource violates a database constraint"),
    ).not.toBeInTheDocument();
  });

  it("assigns an ungrouped device to a group", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    // Orion 03 has no active membership, so it is visible in the computed
    // Ungrouped region without any persisted Ungrouped authority.
    const ungrouped = within(screen.getByRole("region", { name: "Ungrouped" }));
    expect(ungrouped.getByText("Orion 03")).toBeInTheDocument();

    await user.click(
      ungrouped.getByRole("button", { name: "Assign unassigned devices" }),
    );
    const dialog = within(
      screen.getByRole("dialog", { name: "Assign unassigned devices" }),
    );
    const targetGroup = dialog.getByRole("combobox", { name: "Target group" });
    expect(targetGroup).toHaveTextContent("Rack A");
    expect(targetGroup).toHaveClass("w-full");
    await user.click(dialog.getByRole("checkbox", { name: /Orion 03/i }));
    await user.click(dialog.getByRole("button", { name: "Assign 1 device" }));
    rerender(view());

    const active = client
      .getSnapshot()
      .memberships.find(
        (membership) =>
          membership.deviceId === "orion-03" && membership.state === "active",
      );
    expect(active?.groupId).toBe("group-rack-a");
    expect(
      within(screen.getByRole("region", { name: "Rack A" })).getByText(
        "Orion 03",
      ),
    ).toBeInTheDocument();
    expect(
      client.getSnapshot().groups.some((group) => group.id === "ungrouped"),
    ).toBe(false);
  });

  it("assigns a device into a group in place", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackC = within(screen.getByRole("region", { name: "Rack C" }));
    await user.click(rackC.getByRole("button", { name: "Assign devices" }));
    const dialog = within(
      screen.getByRole("dialog", { name: "Assign devices to Rack C" }),
    );
    await user.click(dialog.getByRole("checkbox", { name: /Orion 03/i }));
    await user.click(dialog.getByRole("button", { name: "Assign 1 device" }));
    rerender(view());

    const active = client
      .getSnapshot()
      .memberships.find(
        (membership) =>
          membership.deviceId === "orion-03" && membership.state === "active",
      );
    expect(active?.groupId).toBe("group-rack-c");
  });

  it("reorders devices within a group", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackA = within(screen.getByRole("region", { name: "Rack A" }));
    await user.click(rackA.getByRole("button", { name: "Move Atlas 07 up" }));
    rerender(view());

    const positions = client
      .getSnapshot()
      .memberships.filter(
        (membership) =>
          membership.groupId === "group-rack-a" &&
          membership.state === "active",
      )
      .sort((left, right) => left.position - right.position);
    expect(positions.map((membership) => membership.deviceId)).toEqual([
      "atlas-07",
      "atlas-04",
    ]);
  });

  it("removes a device from a group so it returns to the computed Ungrouped view", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackA = within(screen.getByRole("region", { name: "Rack A" }));
    await user.click(
      rackA.getByRole("button", { name: "Remove Atlas 04 from Rack A" }),
    );
    rerender(view());

    const stillActive = client
      .getSnapshot()
      .memberships.some(
        (membership) =>
          membership.deviceId === "atlas-04" && membership.state === "active",
      );
    expect(stillActive).toBe(false);
    expect(
      within(screen.getByRole("region", { name: "Ungrouped" })).getByText(
        "Atlas 04",
      ),
    ).toBeInTheDocument();
  });

  it("renames a group from the group section", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackB = within(screen.getByRole("region", { name: "Rack B" }));
    expect(rackB.queryByRole("textbox")).not.toBeInTheDocument();
    await user.click(rackB.getByRole("button", { name: "Rename Rack B" }));
    const dialog = within(screen.getByRole("dialog", { name: "Rename group" }));
    await user.clear(dialog.getByLabelText("Group name"));
    await user.type(dialog.getByLabelText("Group name"), "Rack E");
    await user.click(dialog.getByRole("button", { name: "Save name" }));
    rerender(view());

    expect(
      client.getSnapshot().groups.find((group) => group.id === "group-rack-b")
        ?.name,
    ).toBe("Rack E");
    expect(screen.getByRole("region", { name: "Rack E" })).toBeInTheDocument();
  });

  it("keeps a duplicate rename error inside the rename dialog", async () => {
    const user = userEvent.setup();
    const { view } = harness();
    render(view());

    const rackB = within(screen.getByRole("region", { name: "Rack B" }));
    await user.click(rackB.getByRole("button", { name: "Rename Rack B" }));
    const dialog = within(screen.getByRole("dialog", { name: "Rename group" }));
    const input = dialog.getByLabelText("Group name");
    await user.clear(input);
    await user.type(input, "Rack A");
    await user.click(dialog.getByRole("button", { name: "Save name" }));

    expect(dialog.getByRole("alert")).toHaveTextContent(
      "A group with this name already exists. Choose a different name.",
    );
    expect(
      screen.getByRole("dialog", { name: "Rename group" }),
    ).toBeInTheDocument();
  });

  it("deletes a group from the group section", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackC = within(screen.getByRole("region", { name: "Rack C" }));
    await user.click(rackC.getByRole("button", { name: "Delete Rack C" }));
    await user.click(
      within(screen.getByRole("dialog", { name: "Delete Rack C?" })).getByRole(
        "button",
        { name: "Delete group" },
      ),
    );
    rerender(view());

    expect(
      client.getSnapshot().groups.some((group) => group.id === "group-rack-c"),
    ).toBe(false);
    expect(
      screen.queryByRole("region", { name: "Rack C" }),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Ungrouped" })).getByText(
        "Orion 01",
      ),
    ).toBeInTheDocument();
  });

  it("reorders the groups themselves", async () => {
    const user = userEvent.setup();
    const { client, view } = harness();
    const { rerender } = render(view());

    const rackC = within(screen.getByRole("region", { name: "Rack C" }));
    await user.click(rackC.getByRole("button", { name: "Move Rack C up" }));
    rerender(view());

    const ordered = [...client.getSnapshot().groups]
      .filter((group) => group.state === "active")
      .sort((left, right) => left.position - right.position);
    expect(ordered.map((group) => group.name)).toEqual([
      "Rack A",
      "Rack C",
      "Rack B",
    ]);
  });

  it("uses a toast for successful assignment without persistent card feedback", async () => {
    const user = userEvent.setup();
    const { view } = harness();
    render(view());

    const rackC = within(screen.getByRole("region", { name: "Rack C" }));
    await user.click(rackC.getByRole("button", { name: "Assign devices" }));
    const dialog = within(
      screen.getByRole("dialog", { name: "Assign devices to Rack C" }),
    );
    await user.click(dialog.getByRole("checkbox", { name: /Orion 03/i }));
    await user.click(dialog.getByRole("button", { name: "Assign 1 device" }));

    expect(
      await screen.findByText("Device assigned to group."),
    ).toBeInTheDocument();
    expect(document.querySelector("[data-feedback-scope]")).toBeNull();
  });
});
