import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router-dom";
import { AntdProvider } from "./app/AntdProvider";
import { AuthProvider } from "./lib/auth";
import { PreferencesProvider } from "./lib/preferences";
import { SiteSettingsProvider } from "./lib/siteSettings";
import { router } from "./app/router";
import "./styles.css";
import "./app/layout.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider>
      <SiteSettingsProvider>
        <PreferencesProvider>
          <AntdProvider>
            <RouterProvider router={router} />
          </AntdProvider>
        </PreferencesProvider>
      </SiteSettingsProvider>
    </AuthProvider>
  </StrictMode>,
);
