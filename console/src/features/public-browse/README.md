# Public browse

The page owns URL and selection state; sections own rendering and their derived
row models or repository usage. Catalog and artifact queries pass cancellation
signals through the shared query layer. Anonymous refusals do not end a signed-in
session. Refresh failures retain loaded content and offer Retry.

Results and usage have independent section boundaries. Retry returns all source
refetch promises, and recovery keys follow the source answers; navigation still
clears the previous section's failure immediately. OCI/Conan incremental state
remains in its extracted hook for a separate query migration, as agreed in
issue 275.

This structural split preserves the committed visual baseline. The catalog
introduction keeps its existing 32px header-to-hero separation because both the
header and primary hero contribute a lead offset. Repository workspaces use
24px after the header and 16px between related surfaces. Results and usage are
20px apart, side by side on desktop and stacked on narrow screens; refresh
warnings are 16px above the retained results. The layout regression bounds each
of these gaps rather than weakening the shared layout rules or updating images.

Format capability registration (issue 272) and the shared ListPage skeleton
(issue 274) remain follow-up integrations. The split's format constants are not
a capability registry. Page messages use the existing typed translator through
the feature dictionaries; the rest of issue 276 is still separate work.
