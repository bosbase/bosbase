<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import Field from "@/components/base/Field.svelte";
    import SchemaField from "@/components/collections/schema/SchemaField.svelte";

    export let field;
    export let key = "";
</script>

<SchemaField bind:field {key} on:rename on:remove on:duplicate {...$$restProps}>
    <svelte:fragment slot="options">
        <Field class="form-field m-b-sm" name="fields.{key}.maxSize" let:uniqueId>
            <label for={uniqueId}>{$_("common.placeholder.fileSizeLimit")}</label>
            <input
                type="number"
                id={uniqueId}
                step="1"
                min="0"
                value={field.maxSize || ""}
                on:input={(e) => (field.maxSize = e.target.value << 0)}
                placeholder={$_("common.message.defaultValue", { values: { value: "max ~5MB" } })}
            />
        </Field>

        <Field class="form-field form-field-toggle" name="fields.{key}.convertURLs" let:uniqueId>
            <input type="checkbox" id={uniqueId} bind:checked={field.convertURLs} />
            <label for={uniqueId}>
                <span class="txt">{$_("common.switch.stripUrlDomain.name")}</span>
                <i
                    class="ri-information-line link-hint"
                    use:tooltip={{
                        text: $_("common.switch.stripUrlDomain.tip"),
                    }}
                />
            </label>
        </Field>
    </svelte:fragment>
</SchemaField>
