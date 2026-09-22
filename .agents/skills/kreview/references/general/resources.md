# Resources

- Establish who owns each resource and how long it must remain available.
  Follow files, processes, connections, tasks, guards, and cancellation scopes
  through cleanup. Flag concrete leaks or lifetimes that exceed their owner.
- Check whether callers share mutable state or duplicate a resource whose
  identity matters. Judge allocation and copying by their actual cost and use.
