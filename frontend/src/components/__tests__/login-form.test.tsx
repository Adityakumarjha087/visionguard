import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { LoginForm, validateCredentials } from "../auth/login-form";

describe("LoginForm", () => {
  it("shows validation errors and does not submit invalid input", async () => {
    const onSubmit = vi.fn();
    render(<LoginForm onSubmit={onSubmit} />);
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByText("Email is required")).toBeInTheDocument();
    expect(screen.getByText("Password is required")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Email"), "not-an-email");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByText("Enter a valid email address")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-invalid", "true");
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits trimmed credentials", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<LoginForm onSubmit={onSubmit} />);
    await userEvent.type(screen.getByLabelText("Email"), " operator@visionguard.dev ");
    await userEvent.type(screen.getByLabelText("Password"), "operator12345");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(onSubmit).toHaveBeenCalledWith("operator@visionguard.dev", "operator12345");
  });

  it("shows the server error message", async () => {
    const onSubmit = vi.fn().mockRejectedValue(new ApiError(401, "unauthorized", "Invalid email or password"));
    render(<LoginForm onSubmit={onSubmit} />);
    await userEvent.type(screen.getByLabelText("Email"), "a@b.co");
    await userEvent.type(screen.getByLabelText("Password"), "wrong");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid email or password");
  });

  it("fills a demo account from the hint", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<LoginForm onSubmit={onSubmit} />);
    expect(screen.getByText("admin@visionguard.dev")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Use viewer demo account" }));
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(onSubmit).toHaveBeenCalledWith("viewer@visionguard.dev", "viewer12345");
  });

  it("enforces a minimum password length for registration", () => {
    expect(validateCredentials("a@b.co", "short", 8).password).toMatch(/at least 8/);
    expect(validateCredentials("a@b.co", "longenough", 8)).toEqual({});
  });
});
