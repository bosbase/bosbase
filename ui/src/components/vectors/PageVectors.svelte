<script>
    import { _ } from "svelte-i18n";
    import { onMount, tick } from "svelte";
    import { pageTitle } from "@/stores/app";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import { setErrors } from "@/stores/errors";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import Field from "@/components/base/Field.svelte";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import RefreshButton from "@/components/base/RefreshButton.svelte";
    import Searchbar from "@/components/base/Searchbar.svelte";
    import SortHeader from "@/components/base/SortHeader.svelte";
    import VectorManagementPanel from "./VectorManagementPanel.svelte";

    $pageTitle = $_("page.vector.title");

    let allCollections = [];
    let filteredCollections = [];
    let visibleCollections = [];
    let isLoading = false;
    let createPanel;
    let editPanel;
    let vectorManagementPanel;
    let searchFilter = "";
    let sort = "-id"; // Default to descending by ID
    let page = 1;
    let perPage = 10;
    let formData = {
        name: "",
        dimension: 384,
        distance: "cosine",
    };
    let editingCollection = null;
    let isEditing = false;

    onMount(() => {
        loadCollections();
    });

    async function loadCollections() {
        isLoading = true;
        try {
            const response = await ApiClient.send("/api/vectors/collections", {
                method: "GET",
            });
            allCollections = response || [];
            applySearchFilter();
        } catch (err) {
            ApiClient.error(err);
        }
        isLoading = false;
    }

    function applySearchFilter() {
        let filtered;
        if (!searchFilter || searchFilter.trim() === "") {
            filtered = allCollections;
        } else {
            const filterLower = searchFilter.toLowerCase().trim();
            filtered = allCollections.filter((collection) => {
                if (collection.id && collection.id.toLowerCase().includes(filterLower)) {
                    return true;
                }
                if (collection.name && collection.name.toLowerCase().includes(filterLower)) {
                    return true;
                }
                if (collection.distance && collection.distance.toLowerCase().includes(filterLower)) {
                    return true;
                }
                return false;
            });
        }
        page = 1;
        applySort(filtered);
    }

    function applySort(collectionsToSort) {
        if (!sort) {
            filteredCollections = collectionsToSort;
            updatePagination();
            return;
        }

        const sortField = sort.replace(/^[+-]/, "");
        const isDesc = sort.startsWith("-");

        filteredCollections = [...collectionsToSort].sort((a, b) => {
            let aVal = a[sortField];
            let bVal = b[sortField];

            // Handle null/undefined values
            if (aVal == null && bVal == null) return 0;
            if (aVal == null) return isDesc ? 1 : -1;
            if (bVal == null) return isDesc ? -1 : 1;

            // Convert to comparable values
            if (typeof aVal === "string") {
                aVal = aVal.toLowerCase();
            }
            if (typeof bVal === "string") {
                bVal = bVal.toLowerCase();
            }

            if (aVal < bVal) return isDesc ? 1 : -1;
            if (aVal > bVal) return isDesc ? -1 : 1;
            return 0;
        });
        updatePagination();
    }

    function updatePagination() {
        const totalItems = filteredCollections.length;
        const totalPages = Math.max(1, Math.ceil(totalItems / perPage));
        if (page > totalPages) {
            page = totalPages;
        }

        const start = (page - 1) * perPage;
        visibleCollections = filteredCollections.slice(start, start + perPage);
    }

    function changePage(delta) {
        const totalPages = Math.max(1, Math.ceil(filteredCollections.length / perPage));
        const nextPage = Math.min(Math.max(page + delta, 1), totalPages);
        if (nextPage !== page) {
            page = nextPage;
            updatePagination();
        }
    }

    function changePerPage(event) {
        perPage = parseInt(event.target.value, 10) || 10;
        page = 1;
        updatePagination();
    }

    $: if (sort && allCollections.length > 0) {
        applySearchFilter();
    }

    $: totalCollections = filteredCollections.length;
    $: totalPages = Math.max(1, Math.ceil(totalCollections / perPage));
    $: paginationSummary = (() => {
        const template = $_("page.vector.collections.pagination.summary");
        return template
            .replace("{current}", page)
            .replace("{total}", totalPages)
            .replace("{count}", totalCollections);
    })();
    $: paginationSummarySingle = (() => {
        const template = $_("page.vector.collections.pagination.summary");
        return template
            .replace("{current}", "1")
            .replace("{total}", "1")
            .replace("{count}", totalCollections);
    })();

    function handleSearch(e) {
        searchFilter = e.detail || "";
        applySearchFilter();
    }

    function clearSearch() {
        searchFilter = "";
        applySearchFilter();
    }

    function distanceLabel(value) {
        switch (value) {
            case "l2":
                return $_("page.vector.collections.panel.distanceOptions.l2");
            case "inner_product":
                return $_("page.vector.collections.panel.distanceOptions.innerProduct");
            default:
                return $_("page.vector.collections.panel.distanceOptions.cosine");
        }
    }

    async function createCollection() {
        if (!formData.name) {
            setErrors({ name: $_("page.vector.collections.panel.nameRequired") });
            return;
        }

        try {
            await ApiClient.send(`/api/vectors/collections/${encodeURIComponent(formData.name)}`, {
                method: "POST",
                body: {
                    dimension: formData.dimension || 384,
                    distance: formData.distance || "cosine",
                },
            });
            addSuccessToast($_("page.vector.collections.toast.created"));
            createPanel?.hide();
            formData = { name: "", dimension: 384, distance: "cosine" };
            setErrors({});
            await tick();
            await loadCollections();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    async function updateCollection() {
        if (!editingCollection) {
            return;
        }

        try {
            await ApiClient.send(`/api/vectors/collections/${encodeURIComponent(editingCollection.name)}`, {
                method: "PATCH",
                body: {
                    distance: formData.distance || "cosine",
                },
            });
            addSuccessToast($_("page.vector.collections.toast.updated"));
            editPanel?.hide();
            editingCollection = null;
            formData = { name: "", dimension: 384, distance: "cosine" };
            setErrors({});
            await tick();
            await loadCollections();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    function showEditPanel(collection) {
        editingCollection = collection;
        isEditing = true;
        formData = {
            name: collection.name,
            dimension: collection.dimension,
            distance: collection.distance || "cosine",
        };
        setErrors({});
        editPanel?.show();
    }

    async function deleteCollection(name) {
        if (!confirm($_("page.vector.collections.confirmDelete", { name }))) {
            return;
        }

        try {
            await ApiClient.send(`/api/vectors/collections/${encodeURIComponent(name)}`, {
                method: "DELETE",
            });
            addSuccessToast($_("page.vector.collections.toast.deleted"));
            loadCollections();
        } catch (err) {
            ApiClient.error(err);
        }
    }

    function showCreatePanel() {
        isEditing = false;
        editingCollection = null;
        formData = { name: "", dimension: 384, distance: "cosine" };
        setErrors({});
        createPanel?.show();
    }

    function viewCollection(collection) {
        vectorManagementPanel?.show(collection);
    }
</script>

<PageWrapper class="flex-content">
    <header class="page-header">
        <nav class="breadcrumbs">
            <div class="breadcrumb-item">{$pageTitle}</div>
        </nav>

        <div class="inline-flex gap-5">
            <RefreshButton on:refresh={loadCollections} />
        </div>

        <div class="btns-group">
            <button type="button" class="btn btn-expanded" on:click={showCreatePanel}>
                <i class="ri-add-line" />
                <span class="txt">{$_("page.vector.collections.actions.create")}</span>
            </button>
        </div>
    </header>

    <Searchbar
        value={searchFilter}
        placeholder={$_("page.vector.collections.searchPlaceholder")}
        on:submit={handleSearch}
        on:clear={clearSearch}
    />

    <div class="clearfix m-b-sm" />

    {#if isLoading}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            <i class="ri-loader-4-line spin" />
            <span class="m-l-sm">{$_("page.vector.collections.loading")}</span>
        </div>
    {:else if filteredCollections.length === 0}
        <div class="m-t-xl m-b-xl txt-center txt-hint">
            {#if searchFilter}
                <p>{$_("page.vector.collections.noMatch", { value: searchFilter })}</p>
                <p class="m-t-sm">
                    <button type="button" class="btn btn-sm btn-outline" on:click={clearSearch}>
                        {$_("page.vector.collections.actions.clearSearch")}
                    </button>
                </p>
            {:else}
                <p>{$_("page.vector.collections.noData")}</p>
                <p class="m-t-sm">{$_("page.vector.collections.noDataHint")}</p>
            {/if}
        </div>
    {:else}
        <div class="table-wrapper">
            <table class="table">
                <thead>
                    <tr>
                        <SortHeader name="id" bind:sort>
                            {$_("page.vector.collections.table.id")}
                        </SortHeader>
                        <th>{$_("page.vector.collections.table.name")}</th>
                        <th>{$_("page.vector.collections.table.dimension")}</th>
                        <th>{$_("page.vector.collections.table.distance")}</th>
                        <th>{$_("page.vector.collections.table.vectors")}</th>
                        <th class="txt-right">{$_("page.vector.collections.table.actions")}</th>
                    </tr>
                </thead>
                <tbody>
                    {#each visibleCollections as collection}
                        <tr class="collection-row" style="cursor: pointer;" on:click={() => viewCollection(collection)}>
                            <td>
                                {#if collection.id}
                                    <span class="txt-mono txt-sm" title={collection.id}>{collection.id}</span>
                                {:else}
                                    <span class="txt-hint">—</span>
                                {/if}
                            </td>
                            <td>
                                <strong>{collection.name}</strong>
                            </td>
                            <td>{collection.dimension}</td>
                            <td>
                                <span class="badge badge-blue">{distanceLabel(collection.distance)}</span>
                            </td>
                            <td>{collection.count || 0}</td>
                            <td class="txt-right">
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm"
                                    on:click|stopPropagation={() => viewCollection(collection)}
                                    title={$_("page.vector.collections.actions.manage")}
                                >
                                    <i class="ri-list-check" />
                                </button>
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm"
                                    on:click|stopPropagation={() => showEditPanel(collection)}
                                    title={$_("page.vector.collections.actions.edit")}
                                >
                                    <i class="ri-edit-line" />
                                </button>
                                <button
                                    type="button"
                                    class="btn btn-transparent btn-sm txt-danger"
                                    on:click|stopPropagation={() => deleteCollection(collection.name)}
                                    title={$_("page.vector.collections.actions.delete")}
                                >
                                    <i class="ri-delete-bin-line" />
                                </button>
                            </td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>

        <div class="table-controls m-t-sm">
            <label class="controls-label">
                {$_("page.vector.collections.pagination.perPage")}
            </label>
            <select class="form-control form-control-sm" on:change={changePerPage} bind:value={perPage}>
                <option value="10">10</option>
                <option value="25">25</option>
                <option value="50">50</option>
            </select>
        </div>

        {#if totalPages > 1}
            <div class="table-pagination m-t-sm">
                <button
                    type="button"
                    class="btn btn-sm btn-outline"
                    on:click={() => changePage(-1)}
                    disabled={page === 1}
                >
                    {$_("page.vector.collections.pagination.previous")}
                </button>
                <span class="txt-hint m-l-sm m-r-sm">
                    {paginationSummary}
                </span>
                <button
                    type="button"
                    class="btn btn-sm btn-outline"
                    on:click={() => changePage(1)}
                    disabled={page === totalPages || totalCollections === 0}
                >
                    {$_("page.vector.collections.pagination.next")}
                </button>
            </div>
        {:else if totalCollections > 0}
            <div class="table-pagination m-t-sm">
                <span class="txt-hint">
                    {paginationSummarySingle}
                </span>
            </div>
        {/if}
    {/if}
</PageWrapper>

<OverlayPanel bind:this={createPanel} title={$_("page.vector.collections.panel.createTitle")}>
    <form on:submit|preventDefault={createCollection}>
        <Field class="form-field required" name="name" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.nameLabel")}</span>
            </label>
            <input
                type="text"
                id={uniqueId}
                required
                placeholder={$_("page.vector.collections.panel.namePlaceholder")}
                bind:value={formData.name}
            />
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.nameHelp")}</p>
            </div>
        </Field>

        <Field class="form-field required" name="dimension" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.dimensionLabel")}</span>
            </label>
            <input
                type="number"
                id={uniqueId}
                required
                min="1"
                max="65535"
                step="1"
                placeholder={$_("page.vector.collections.panel.dimensionPlaceholder")}
                bind:value={formData.dimension}
            />
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.dimensionHelp")}</p>
            </div>
        </Field>

        <Field class="form-field required" name="distance" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.distanceLabel")}</span>
            </label>
            <select
                id={uniqueId}
                required
                bind:value={formData.distance}
            >
                <option value="cosine">{$_("page.vector.collections.panel.distanceOptions.cosine")}</option>
                <option value="l2">{$_("page.vector.collections.panel.distanceOptions.l2")}</option>
                <option value="inner_product">{$_("page.vector.collections.panel.distanceOptions.innerProduct")}</option>
            </select>
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.distanceHelp")}</p>
            </div>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded">
                <i class="ri-check-line" />
                <span class="txt">{$_("page.vector.collections.panel.create")}</span>
            </button>
            <button type="button" class="btn btn-outline" on:click={() => createPanel?.hide()}>
                <span class="txt">{$_("common.action.cancel")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>

<OverlayPanel bind:this={editPanel} title={$_("page.vector.collections.panel.editTitle")}>
    <form on:submit|preventDefault={updateCollection}>
        <Field class="form-field" name="name" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.nameLabel")}</span>
            </label>
            <input
                type="text"
                id={uniqueId}
                value={formData.name}
                disabled
            />
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.nameLocked")}</p>
            </div>
        </Field>

        <Field class="form-field" name="dimension" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.dimensionLabel")}</span>
            </label>
            <input
                type="number"
                id={uniqueId}
                value={formData.dimension}
                disabled
            />
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.dimensionLocked")}</p>
            </div>
        </Field>

        <Field class="form-field required" name="distance" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.collections.panel.distanceLabel")}</span>
            </label>
            <select
                id={uniqueId}
                required
                bind:value={formData.distance}
            >
                <option value="cosine">{$_("page.vector.collections.panel.distanceOptions.cosine")}</option>
                <option value="l2">{$_("page.vector.collections.panel.distanceOptions.l2")}</option>
                <option value="inner_product">{$_("page.vector.collections.panel.distanceOptions.innerProduct")}</option>
            </select>
            <div class="help-block">
                <p>{$_("page.vector.collections.panel.distanceHelp")}</p>
            </div>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded">
                <i class="ri-check-line" />
                <span class="txt">{$_("page.vector.collections.panel.update")}</span>
            </button>
            <button type="button" class="btn btn-outline" on:click={() => editPanel?.hide()}>
                <span class="txt">{$_("common.action.cancel")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>

<VectorManagementPanel bind:this={vectorManagementPanel} />

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
</style>
