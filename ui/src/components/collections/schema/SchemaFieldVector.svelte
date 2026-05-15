<script>
    import { _ } from "svelte-i18n";
    import Field from "@/components/base/Field.svelte";
    import SchemaField from "@/components/collections/schema/SchemaField.svelte";

    export let field;
    export let key = "";

    $: distanceOptions = [
        { label: $_("common.database.fieldPreset.vectorDistance.cosine"), value: "cosine" },
        { label: $_("common.database.fieldPreset.vectorDistance.l2"), value: "l2" },
        { label: $_("common.database.fieldPreset.vectorDistance.innerProduct"), value: "inner_product" },
    ];
</script>

<SchemaField bind:field {key} on:rename on:remove on:duplicate {...$$restProps}>
    <svelte:fragment slot="options">
        <Field class="form-field required m-b-sm" name="fields.{key}.dimension" let:uniqueId>
            <label for={uniqueId}>{$_("common.database.fieldPreset.vectorDimension")}</label>
            <input
                type="number"
                id={uniqueId}
                step="1"
                min="1"
                value={field.dimension || 1536}
                on:input={(e) => (field.dimension = e.target.value << 0)}
                placeholder="1536"
            />
            <div class="help-block">{$_("common.database.fieldPreset.vectorDimensionHint")}</div>
        </Field>

        <Field class="form-field m-b-sm" name="fields.{key}.distance" let:uniqueId>
            <label for={uniqueId}>{$_("common.database.fieldPreset.vectorDistanceLabel")}</label>
            <select id={uniqueId} value={field.distance || "cosine"} on:change={(e) => (field.distance = e.target.value)}>
                {#each distanceOptions as opt}
                    <option value={opt.value}>{opt.label}</option>
                {/each}
            </select>
            <div class="help-block">{$_("common.database.fieldPreset.vectorDistanceHint")}</div>
        </Field>
    </svelte:fragment>
</SchemaField>
