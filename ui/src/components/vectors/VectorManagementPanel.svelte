<script>
    import { _ } from "svelte-i18n";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import VectorUpsertPanel from "./VectorUpsertPanel.svelte";
    import VectorImportPanel from "./VectorImportPanel.svelte";
    import Searchbar from "@/components/base/Searchbar.svelte";

    let collection = null;

    let vectorsPanel;
    let vectorUpsertPanel;
    let vectorImportPanel;
    let allVectors = [];
    let vectors = [];
    let isLoading = false;
    let page = 1;
    let perPage = 20;
    let totalItems = 0;
    let totalPages = 0;
    let searchFilter = "";

    export function show(collectionData) {
        collection = collectionData;
        page = 1;
        searchFilter = "";
        vectorsPanel?.show();
        loadVectors();
    }

    function hide() {
        vectorsPanel?.hide();
    }

    async function loadVectors() {
        if (!collection) return;

        isLoading = true;
        try {
            const response = await ApiClient.send(`/api/vectors/${encodeURIComponent(collection.name)}?page=1&perPage=1000`, {
                method: "GET",
            });
            allVectors = response.items || [];
            totalItems = response.totalItems || 0;
            applyFilter();
        } catch (err) {
            ApiClient.error(err);
        }
        isLoading = false;
    }

    function applyFilter() {
        let filteredVectors;

        if (!searchFilter || searchFilter.trim() === "") {
            filteredVectors = allVectors;
            totalItems = allVectors.length;
        } else {
            const filterLower = searchFilter.toLowerCase().trim();
            filteredVectors = allVectors.filter((vector) => {
                if (vector.id && vector.id.toLowerCase().includes(filterLower)) {
                    return true;
                }
                if (vector.content && vector.content.toLowerCase().includes(filterLower)) {
                    return true;
                }
                if (vector.metadata) {
                    const metadataStr = JSON.stringify(vector.metadata).toLowerCase();
                    if (metadataStr.includes(filterLower)) {
                        return true;
                    }
                }
                return false;
            });
            totalItems = filteredVectors.length;
        }

        totalPages = Math.ceil(totalItems / perPage);

        const start = (page - 1) * perPage;
        const end = start + perPage;
        vectors = filteredVectors.slice(start, end);
    }

    function handleSearch(e) {
        searchFilter = e.detail || "";
        page = 1;
        applyFilter();
    }

    function clearSearch() {
        searchFilter = "";
        page = 1;
        applyFilter();
    }

    function showAddVector() {
        vectorUpsertPanel?.show(collection, null);
    }

    function showImportVectors() {
        vectorImportPanel?.show(collection);
    }

    function editVector(vector) {
        vectorUpsertPanel?.show(collection, vector);
    }

    async function deleteVector(id) {
        if (!confirm($_("page.vector.management.confirmDelete"))) {
            return;
        }

        try {
            await ApiClient.send(`/api/vectors/${encodeURIComponent(collection.name)}/${encodeURIComponent(id)}`, {
                method: "DELETE",
            });
            addSuccessToast($_("page.vector.management.toast.deleted"));
            loadVectors();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    async function onVectorSaved() {
        page = 1;
        await loadVectors();
    }

    async function onVectorsImported() {
        page = 1;
        await loadVectors();
    }
</script>

<OverlayPanel
    bind:this={vectorsPanel}
    title={$_("page.vector.management.title", { name: collection?.name || "" })}
    class="vector-management-panel"
>
    <div class="panel-header-actions">
        <button type="button" class="btn btn-sm" on:click={showAddVector}>
            <i class="ri-add-line" />
            <span class="txt">{$_("page.vector.management.actions.add")}</span>
        </button>
        <button type="button" class="btn btn-sm btn-outline" on:click={showImportVectors}>
            <i class="ri-upload-line" />
            <span class="txt">{$_("page.vector.management.actions.import")}</span>
        </button>
    </div>

    <Searchbar
        value={searchFilter}
        placeholder={$_("page.vector.management.searchPlaceholder")}
        on:submit={handleSearch}
        on:clear={clearSearch}
    />

    <div class="clearfix m-b-sm" />

    {#if isLoading}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            <i class="ri-loader-4-line spin" />
            <span class="m-l-sm">{$_("page.vector.management.loading")}</span>
        </div>
    {:else if vectors.length === 0}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            {#if searchFilter}
                <p>{$_("page.vector.management.noMatch", { value: searchFilter })}</p>
                <p class="m-t-sm">
                    <button type="button" class="btn btn-sm btn-outline" on:click={clearSearch}>
                        {$_("page.vector.collections.actions.clearSearch")}
                    </button>
                </p>
            {:else}
                <p>{$_("page.vector.management.noData")}</p>
                <p class="m-t-sm">{$_("page.vector.management.noDataHint")}</p>
            {/if}
        </div>
    {:else}
        <div class="table-wrapper">
            <table class="table">
                <thead>
                    <tr>
                        <th>{$_("page.vector.management.table.id")}</th>
                        <th>{$_("page.vector.management.table.content")}</th>
                        <th>{$_("page.vector.management.table.metadata")}</th>
                        <th class="txt-right">{$_("page.vector.management.table.actions")}</th>
                    </tr>
                </thead>
                <tbody>
                    {#each vectors as vector}
                        <tr>
                            <td>
                                <code class="txt-sm">{vector.id}</code>
                            </td>
                            <td>
                                {#if vector.content && vector.content.trim() !== ""}
                                    <span class="txt-ellipsis" style="max-width: 200px; display: inline-block;">
                                        {vector.content}
                                    </span>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td>
                                {#if vector.metadata && Object.keys(vector.metadata).length > 0}
                                    <code class="txt-xs">{JSON.stringify(vector.metadata)}</code>
                                {:else}
                                    <span class="txt-hint">-</span>
                                {/if}
                            </td>
                            <td class="txt-right">
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm"
                                    on:click={() => editVector(vector)}
                                    title={$_("page.vector.management.table.edit")}
                                >
                                    <i class="ri-edit-line" />
                                </button>
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm txt-danger"
                                    on:click={() => deleteVector(vector.id)}
                                    title={$_("page.vector.management.table.delete")}
                                >
                                    <i class="ri-delete-bin-line" />
                                </button>
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>

        {#if totalPages > 1}
            <div class="pagination m-t-lg">
                <button
                    type="button"
                    class="btn btn-sm"
                    disabled={page <= 1}
                    on:click={() => {
                        page--;
                        applyFilter();
                    }}
                >
                    <i class="ri-arrow-left-line" />
                    <span class="txt">{$_("page.vector.management.pagination.previous")}</span>
                </button>
                <span class="txt-hint txt-sm m-l-sm m-r-sm">
                    {$_("page.vector.management.pagination.summary", {
                        current: page,
                        total: totalPages,
                        count: totalItems,
                    })}
                </span>
                <button
                    type="button"
                    class="btn btn-sm"
                    disabled={page >= totalPages}
                    on:click={() => {
                        page++;
                        applyFilter();
                    }}
                >
                    <span class="txt">{$_("page.vector.management.pagination.next")}</span>
                    <i class="ri-arrow-right-line" />
                </button>
            </div>
        {/if}
    {/if}
</OverlayPanel>

<VectorUpsertPanel bind:this={vectorUpsertPanel} {collection} on:saved={onVectorSaved} />
<VectorImportPanel bind:this={vectorImportPanel} {collection} on:imported={onVectorsImported} />

<style>
    .spin {
        animation: spin 1s linear infinite;
    }

    @keyframes spin {
        from {
            transform: rotate(0deg);
        }
        to {
            transform: rotate(360deg);
        }
    }

    .panel-header-actions {
        display: flex;
        gap: 0.5rem;
        margin-bottom: 1rem;
    }

    .pagination {
        display: flex;
        align-items: center;
        justify-content: center;
    }
</style>
