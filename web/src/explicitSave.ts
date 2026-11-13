// Enter in a data-entry field must not submit a mutation form implicitly.
// Buttons remain keyboard accessible. Queries keep their usual Enter behavior.
export function preventImplicitSave(event:KeyboardEvent) {
 if(event.key!=='Enter'||event.isComposing||event.ctrlKey||event.metaKey||!(event.target instanceof Element))return
 const field=event.target.closest('input,select'),form=field?.closest('form')
 if(form&&!form.matches('.filter-bar,.search-form,.revision-compare-form'))event.preventDefault()
}
