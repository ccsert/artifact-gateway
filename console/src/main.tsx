import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router-dom";
import { AntdProvider } from "./app/AntdProvider";
import { AuthProvider } from "./lib/auth";
import { PreferencesProvider } from "./lib/preferences";
import { router } from "./app/router";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PreferencesProvider>
      <AntdProvider>
        <AuthProvider>
          <RouterProvider router={router} />
        </AuthProvider>
      </AntdProvider>
    </PreferencesProvider>
  </StrictMode>,
);
